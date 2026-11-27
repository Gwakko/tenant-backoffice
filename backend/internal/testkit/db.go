// Package testkit holds helpers shared by tests: real databases cloned from a
// migrated template.
package testkit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/migrations"
)

// advisoryLockKey serialises role setup, template creation and cloning across
// test processes that share one Postgres server.
const advisoryLockKey int64 = 0x7462_7465_7374 // "tbtest"

// DB is one throwaway database cloned from the migrated template.
type DB struct {
	Name string
	// Owner connects as tb_owner (migrations, snapshots, seeding).
	Owner *pgxpool.Pool
	// App connects as tb_app, the role the service runs with.
	App *pgxpool.Pool
}

// NewDB creates a database from the template (building the template first if
// its migrations hash is new) and drops it when the test ends. It fails the
// test when TB_TEST_DB_ADMIN_URL is not set; it never skips.
func NewDB(tb testing.TB) *DB {
	tb.Helper()
	return newDB(tb, true)
}

// NewEmptyDB creates a database without any migrations applied, owned by
// tb_owner, and drops it when the test ends.
func NewEmptyDB(tb testing.TB) *DB {
	tb.Helper()
	return newDB(tb, false)
}

func newDB(tb testing.TB, fromTemplate bool) *DB {
	tb.Helper()
	adminURL, ok := config.TestDBAdminURL()
	if !ok {
		tb.Fatal("TB_TEST_DB_ADMIN_URL is required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		tb.Fatalf("testkit: connect admin: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	name := fmt.Sprintf("tb_t_%08x%08x", rand.Uint32(), rand.Uint32())
	if err := createDatabase(ctx, admin, adminURL, name, fromTemplate); err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	db := &DB{Name: name}
	tb.Cleanup(func() {
		if db.Owner != nil {
			db.Owner.Close()
		}
		if db.App != nil {
			db.App.Close()
		}
		c, err := pgx.Connect(context.Background(), adminURL)
		if err != nil {
			tb.Errorf("testkit: connect admin for drop: %v", err)
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quote(name)+" WITH (FORCE)"); err != nil {
			tb.Errorf("testkit: drop %s: %v", name, err)
		}
	})
	if db.Owner, err = pool(ctx, adminURL, name, "tb_owner"); err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	if db.App, err = pool(ctx, adminURL, name, "tb_app"); err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	return db
}

// URL returns a connection URL of this database with the admin credentials of
// TB_TEST_DB_ADMIN_URL (tests that need a URL, not a pool).
func (d *DB) URL(tb testing.TB) string {
	tb.Helper()
	adminURL, ok := config.TestDBAdminURL()
	if !ok {
		tb.Fatal("TB_TEST_DB_ADMIN_URL is required")
	}
	u, err := withDatabase(adminURL, d.Name)
	if err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	return u
}

// AppURL returns a connection URL of this database that runs every session as
// tb_app (the role is set at connection start, so row-level security applies
// although the credentials are the admin ones). Use it where a component takes
// a URL, like the application pool of the API.
func (d *DB) AppURL(tb testing.TB) string {
	tb.Helper()
	u := d.URL(tb)
	parsed, err := url.Parse(u)
	if err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	q := parsed.Query()
	q.Set("role", "tb_app")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// NewAppPool opens an extra pool as tb_app with at most maxConns connections
// (a pool of one proves that a connection is reused between transactions). The
// pool is closed when the test ends.
func (d *DB) NewAppPool(tb testing.TB, maxConns int32) *pgxpool.Pool {
	tb.Helper()
	adminURL, ok := config.TestDBAdminURL()
	if !ok {
		tb.Fatal("TB_TEST_DB_ADMIN_URL is required")
	}
	p, err := poolWithMax(context.Background(), adminURL, d.Name, "tb_app", maxConns)
	if err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	tb.Cleanup(p.Close)
	return p
}

// Exists reports whether a database with this name exists on the server.
func Exists(ctx context.Context, tb testing.TB, name string) bool {
	tb.Helper()
	adminURL, ok := config.TestDBAdminURL()
	if !ok {
		tb.Fatal("TB_TEST_DB_ADMIN_URL is required")
	}
	c, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		tb.Fatalf("testkit: connect admin: %v", err)
	}
	defer func() { _ = c.Close(ctx) }()
	var n int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", name).Scan(&n); err != nil {
		tb.Fatalf("testkit: %v", err)
	}
	return n > 0
}

// pool opens a pool that switches to role after connecting. The admin
// credentials are reused, so the shared roles never get their passwords changed
// and parallel test packages cannot lock each other out.
func pool(ctx context.Context, adminURL, dbName, role string) (*pgxpool.Pool, error) {
	return poolWithMax(ctx, adminURL, dbName, role, 0)
}

func poolWithMax(ctx context.Context, adminURL, dbName, role string, maxConns int32) (*pgxpool.Pool, error) {
	u, err := withDatabase(adminURL, dbName)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(u)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+quote(role))
		return err
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool as %s: %w", role, err)
	}
	return p, nil
}

func createDatabase(ctx context.Context, admin *pgx.Conn, adminURL, name string, fromTemplate bool) error {
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		_, _ = admin.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if err := ensureRoles(ctx, admin); err != nil {
		return err
	}
	q := "CREATE DATABASE " + quote(name) + " OWNER tb_owner"
	if fromTemplate {
		tpl, err := ensureTemplate(ctx, admin, adminURL)
		if err != nil {
			return err
		}
		q = "CREATE DATABASE " + quote(name) + " TEMPLATE " + quote(tpl) + " OWNER tb_owner"
	}
	if _, err := admin.Exec(ctx, q); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	return nil
}

// ensureRoles creates the application roles when missing, mirroring
// db/bootstrap/roles.sql without passwords: tests reach them through SET ROLE.
func ensureRoles(ctx context.Context, admin *pgx.Conn) error {
	for _, r := range []string{"tb_owner", "tb_app"} {
		var n int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_roles WHERE rolname = $1", r).Scan(&n); err != nil {
			return fmt.Errorf("check role %s: %w", r, err)
		}
		if n == 0 {
			q := "CREATE ROLE " + quote(r) + " LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE"
			if _, err := admin.Exec(ctx, q); err != nil {
				return fmt.Errorf("create role %s: %w", r, err)
			}
		}
	}
	return nil
}

func ensureTemplate(ctx context.Context, admin *pgx.Conn, adminURL string) (string, error) {
	hash, err := migrationsHash(migrations.FS)
	if err != nil {
		return "", err
	}
	tpl := "tb_tpl_" + hash
	var n int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", tpl).Scan(&n); err != nil {
		return "", fmt.Errorf("check template: %w", err)
	}
	if n > 0 {
		return tpl, nil
	}
	building := tpl + "_building"
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quote(building)+" WITH (FORCE)"); err != nil {
		return "", fmt.Errorf("drop stale template: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quote(building)+" OWNER tb_owner"); err != nil {
		return "", fmt.Errorf("create template: %w", err)
	}
	if err := migrateTemplate(ctx, adminURL, building); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+quote(building)+" RENAME TO "+quote(tpl)); err != nil {
		return "", fmt.Errorf("rename template: %w", err)
	}
	return tpl, nil
}

func migrateTemplate(ctx context.Context, adminURL, dbName string) error {
	u, err := withDatabase(adminURL, dbName)
	if err != nil {
		return err
	}
	cc, err := pgx.ParseConfig(u)
	if err != nil {
		return fmt.Errorf("parse template url: %w", err)
	}
	db := stdlib.OpenDB(*cc, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE tb_owner")
		return err
	}))
	defer func() { _ = db.Close() }()
	if err := grantDefaults(ctx, db); err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("template provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("template migrate: %w", err)
	}
	return nil
}

// grantDefaults gives tb_app access to objects tb_owner creates later, as in roles.sql.
func grantDefaults(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		"GRANT USAGE ON SCHEMA public TO tb_app",
		"ALTER DEFAULT PRIVILEGES FOR ROLE tb_owner IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO tb_app",
		"ALTER DEFAULT PRIVILEGES FOR ROLE tb_owner IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO tb_app",
		"ALTER DEFAULT PRIVILEGES FOR ROLE tb_owner IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO tb_app",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("template grants: %w", err)
		}
	}
	return nil
}

// migrationsHash is the first 12 hex characters of the SHA-256 over every
// migration file name and content, in name order.
func migrationsHash(fsys fs.FS) (string, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return "", fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", n, err)
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

func withDatabase(rawURL, name string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
