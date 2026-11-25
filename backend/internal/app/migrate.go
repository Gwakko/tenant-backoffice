package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/migrations"
)

// Migrator applies goose migrations from a file system to one database.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

// NewMigrator builds a migrator over db. Every migration runs in its own transaction.
func NewMigrator(db *sql.DB, migrationsFS fs.FS) (*Migrator, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS)
	if err != nil {
		return nil, fmt.Errorf("migrator: %w", err)
	}
	return &Migrator{db: db, provider: p}, nil
}

// Up applies all pending migrations.
func (m *Migrator) Up(ctx context.Context) error {
	if _, err := m.provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down reverts the latest applied migration.
func (m *Migrator) Down(ctx context.Context) error {
	if _, err := m.provider.Down(ctx); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// DownTo reverts migrations until the given version is the latest (0 reverts all).
func (m *Migrator) DownTo(ctx context.Context, version int64) error {
	if _, err := m.provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("migrate down-to %d: %w", version, err)
	}
	return nil
}

// Version returns the latest applied version (0 when none).
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	v, err := m.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrate version: %w", err)
	}
	return v, nil
}

// Status returns every known migration with its applied state.
func (m *Migrator) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	st, err := m.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate status: %w", err)
	}
	return st, nil
}

// LeftoverObjects lists objects in schema public that remain after a full down:
// relations (except the goose version table), functions and extensions.
func LeftoverObjects(ctx context.Context, db *sql.DB) ([]string, error) {
	const q = `
SELECT 'relation ' || c.relname FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relname NOT LIKE 'goose_db_version%'
UNION ALL
SELECT 'function ' || p.proname FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
 WHERE n.nspname = 'public'
UNION ALL
SELECT 'extension ' || extname FROM pg_extension WHERE extname <> 'plpgsql'
ORDER BY 1`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("leftover objects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("leftover objects: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RunMigrate is the entry point of cmd/migrate. It reads the environment,
// connects, and runs one command: up, down, down-to <version>, status or roundtrip.
func RunMigrate(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: migrate up|down|down-to <version>|status|roundtrip")
	}
	cfg, err := config.LoadMigrate()
	if err != nil {
		return err
	}
	if args[0] == "roundtrip" {
		return roundtrip(ctx, cfg, out)
	}
	if cfg.MigrateURL == "" {
		return errors.New("TB_DB_MIGRATE_URL is required")
	}
	db, err := openDB(ctx, cfg.MigrateURL, cfg.ConnectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	m, err := NewMigrator(db, migrations.FS)
	if err != nil {
		return err
	}
	switch args[0] {
	case "up":
		err = m.Up(ctx)
	case "down":
		err = m.Down(ctx)
	case "down-to":
		if len(args) != 2 {
			return errors.New("usage: migrate down-to <version>")
		}
		v, perr := strconv.ParseInt(args[1], 10, 64)
		if perr != nil || v < 0 {
			return fmt.Errorf("down-to: invalid version %q", args[1])
		}
		err = m.DownTo(ctx, v)
	case "status":
		return printStatus(ctx, m, out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		return err
	}
	v, err := m.Version(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "%s ok, version %d\n", args[0], v)
	return nil
}

func printStatus(ctx context.Context, m *Migrator, out io.Writer) error {
	st, err := m.Status(ctx)
	if err != nil {
		return err
	}
	for _, s := range st {
		state := "pending"
		if s.State == goose.StateApplied {
			state = "applied " + s.AppliedAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(out, "%04d %-40s %s\n", s.Source.Version, s.Source.Path, state)
	}
	return nil
}

// roundtrip runs up, down to 0 and up again on a fresh scratch database
// created through the admin URL, and always drops it afterwards.
func roundtrip(ctx context.Context, cfg config.Migrate, out io.Writer) (err error) {
	if cfg.AdminURL == "" {
		return errors.New("TB_TEST_DB_ADMIN_URL is required")
	}
	name := fmt.Sprintf("tb_rt_%08x", rand.Uint32())
	admin, err := openDB(ctx, cfg.AdminURL, cfg.ConnectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE `+quoteIdent(name)); err != nil {
		return fmt.Errorf("roundtrip: create database: %w", err)
	}
	defer func() {
		_, derr := admin.ExecContext(context.WithoutCancel(ctx), `DROP DATABASE `+quoteIdent(name)+` WITH (FORCE)`)
		err = errors.Join(err, derr)
	}()
	scratchURL, err := withDatabase(cfg.AdminURL, name)
	if err != nil {
		return err
	}
	db, err := openDB(ctx, scratchURL, cfg.ConnectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	m, err := NewMigrator(db, migrations.FS)
	if err != nil {
		return err
	}
	if err = m.Up(ctx); err != nil {
		return err
	}
	if err = m.DownTo(ctx, 0); err != nil {
		return err
	}
	left, err := LeftoverObjects(ctx, db)
	if err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("roundtrip: objects left after down: %s", strings.Join(left, ", "))
	}
	if err = m.Up(ctx); err != nil {
		return err
	}
	v, err := m.Version(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "roundtrip ok, version %d\n", v)
	return nil
}

// openDB opens a database/sql handle (goose needs one) over the pgx driver and
// waits until the server answers or the timeout expires.
func openDB(ctx context.Context, rawURL string, timeout time.Duration) (*sql.DB, error) {
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		perr := db.PingContext(ctx)
		if perr == nil {
			return db, nil
		}
		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, fmt.Errorf("database not ready after %s: %w", timeout, perr)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func withDatabase(rawURL, name string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse database url: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
