//go:build integration

package app_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

// seedDay is the fixed "now" of the tests, so that timestamps are reproducible.
var seedDay = time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)

func seedHasherConfig(keysDir string) authn.Config {
	return authn.Config{
		KeysDir: keysDir, ActiveKID: "k1", Issuer: "tenant-backoffice", Audience: "tenant-backoffice-api",
		AccessTTL: 10 * time.Minute, Leeway: 30 * time.Second,
		Argon2MemoryKiB: 16, Argon2Time: 1, Argon2Threads: 1, Argon2KeyLen: 32, Argon2SaltLen: 16,
	}
}

func seedHasher(t *testing.T) *authn.Hasher {
	t.Helper()
	h, err := authn.NewHasher(seedHasherConfig(""))
	require.NoError(t, err)
	return h
}

func runSeed(t *testing.T, db *testkit.DB, d app.SeedData, opts app.SeedOptions) (app.SeedReport, error) {
	t.Helper()
	if opts.Now.IsZero() {
		opts.Now = seedDay
	}
	return app.Seed(context.Background(), db.Owner, d, seedHasher(t), opts)
}

// snapshot returns every table of the public schema as sorted JSON text, so
// that two snapshots can be compared for equality.
func snapshot(t *testing.T, db *testkit.DB) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Owner.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'goose_db_version' ORDER BY 1`)
	require.NoError(t, err)
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	require.NotEmpty(t, tables)
	out := map[string]string{}
	for _, tbl := range tables {
		var s string
		require.NoError(t, db.Owner.QueryRow(ctx,
			`SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]')::text FROM `+pgx.Identifier{tbl}.Sanitize()+` t`).Scan(&s))
		out[tbl] = s
	}
	return out
}

func count(t *testing.T, db *testkit.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.Owner.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n))
	return n
}

// Test-ID: T-I-44
// EC-136: a second seed changes nothing and duplicates nothing, even on a later day.
func TestSeedIdempotent(t *testing.T) {
	db := testkit.NewDB(t)
	d := loadDemo(t)

	first, err := runSeed(t, db, d, app.SeedOptions{})
	require.NoError(t, err)
	require.Equal(t, len(d.Nodes), first.Nodes)
	require.Equal(t, len(d.Users), first.Users)
	require.Equal(t, len(d.Operations)+2*len(d.Transfers), first.Operations)
	before := snapshot(t, db)

	second, err := runSeed(t, db, d, app.SeedOptions{})
	require.NoError(t, err)
	require.Equal(t, app.SeedReport{}, second, "nothing may be inserted on a repeated run")
	require.Equal(t, before, snapshot(t, db))

	third, err := runSeed(t, db, d, app.SeedOptions{Now: seedDay.Add(72 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, app.SeedReport{}, third, "a later day must not add rows either")
	require.Equal(t, before, snapshot(t, db))
}

// The seed keeps its promises: tree, owner, balances, chain, transfers.
func TestSeedInvariants(t *testing.T) {
	db := testkit.NewDB(t)
	ctx := context.Background()
	d := loadDemo(t)
	_, err := runSeed(t, db, d, app.SeedOptions{})
	require.NoError(t, err)
	require.NoError(t, app.CheckSeedInvariants(ctx, db.Owner))

	// The data is the one the file describes.
	require.Equal(t, len(d.Nodes), count(t, db, "nodes"))
	require.Equal(t, len(d.Users), count(t, db, "users"))
	require.Equal(t, len(d.Roles), count(t, db, "role_assignments"))
	require.Equal(t, len(d.Settings), count(t, db, "node_settings"))
	var oldest time.Time
	require.NoError(t, db.Owner.QueryRow(ctx, `SELECT min(created_at) FROM operations`).Scan(&oldest))
	require.Equal(t, time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC), oldest.UTC().Truncate(24*time.Hour), "history starts 60 days before the seed day")

	// Each invariant is able to fail: damage the data inside a transaction
	// that is rolled back, then check.
	damage := map[string]struct{ sql, want string }{
		"balance differs from the journal": {`UPDATE balances SET amount = amount + 1`, "equals the sum"},
		"balance version differs":          {`UPDATE balances SET version = version + 1`, "equals the sum"},
		"root owner is gone":               {`DELETE FROM role_assignments WHERE role = 'owner'`, "active owner"},
		"root owner is inactive":           {`UPDATE users SET status = 'inactive'`, "active owner"},
		"chain does not follow time": {
			`INSERT INTO operations (id, node_id, kind, amount, balance_after, comment, actor_id, created_at)
			 SELECT gen_random_uuid(), node_id, 'credit', 10, 10, '', (SELECT id FROM users LIMIT 1), now() FROM balances LIMIT 1`,
			"balance_after follows",
		},
		"transfer leg without a partner": {
			`INSERT INTO operations (id, node_id, kind, amount, balance_after, transfer_id, counterparty_node_id, comment, actor_id, created_at)
			 SELECT gen_random_uuid(), b.node_id, 'transfer_in', 10, 10, gen_random_uuid(),
			        (SELECT id FROM nodes WHERE id <> b.node_id LIMIT 1), '', (SELECT id FROM users LIMIT 1), now()
			   FROM balances b LIMIT 1`,
			"every transfer",
		},
	}
	for name, c := range damage {
		t.Run(name, func(t *testing.T) {
			tx, err := db.Owner.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			_, err = tx.Exec(ctx, c.sql)
			require.NoError(t, err)
			err = app.CheckSeedInvariants(ctx, tx)
			require.Error(t, err)
			require.Contains(t, err.Error(), c.want)
		})
	}
	require.NoError(t, app.CheckSeedInvariants(ctx, db.Owner), "rollbacks left the data intact")
}

// A database that already holds other data is not seeded unless forced.
func TestSeedRefusesForeignData(t *testing.T) {
	db := testkit.NewDB(t)
	ctx := context.Background()
	d := loadDemo(t)
	_, err := db.Owner.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, status, created_at) VALUES ($1, 'someone@elsewhere.test', 'x', 'active', now())`,
		uuid.New())
	require.NoError(t, err)

	_, err = runSeed(t, db, d, app.SeedOptions{})
	require.ErrorIs(t, err, app.ErrSeedForeignData)
	require.Zero(t, count(t, db, "nodes"), "a refused seed writes nothing")
	require.Equal(t, 1, count(t, db, "users"))

	_, err = runSeed(t, db, d, app.SeedOptions{Force: true})
	require.NoError(t, err)
	require.Equal(t, len(d.Users)+1, count(t, db, "users"))
	require.Equal(t, len(d.Nodes), count(t, db, "nodes"))
}

// A seed that fails halfway leaves nothing behind.
func TestSeedIsAtomic(t *testing.T) {
	db := testkit.NewDB(t)
	d := loadDemo(t)
	// A user with the e-mail of a seed user but another id: the insert fails
	// late (after nodes), and -force lets the run get that far.
	_, err := db.Owner.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash, status, created_at) VALUES ($1, $2, 'x', 'active', now())`,
		uuid.New(), d.Users[0].Email)
	require.NoError(t, err)
	_, err = runSeed(t, db, d, app.SeedOptions{Force: true})
	require.Error(t, err)
	require.Zero(t, count(t, db, "nodes"))
	require.Zero(t, count(t, db, "operations"))
}

// Passwords are stored as argon2id hashes only; the demo users can log in.
func TestSeedPasswordsAreHashedAndWork(t *testing.T) {
	db := testkit.NewDB(t)
	ctx := context.Background()
	d := loadDemo(t)
	_, err := runSeed(t, db, d, app.SeedOptions{})
	require.NoError(t, err)

	// No table holds a demo password anywhere in any column.
	rows, err := db.Owner.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	require.NoError(t, err)
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	for _, u := range d.Users {
		for _, tbl := range tables {
			var n int
			require.NoError(t, db.Owner.QueryRow(ctx,
				`SELECT count(*) FROM `+pgx.Identifier{tbl}.Sanitize()+` t WHERE position($1 in t::text) > 0`, u.Password).Scan(&n))
			require.Zero(t, n, "password of %s found in table %s", u.Email, tbl)
		}
	}
	var bad int
	require.NoError(t, db.Owner.QueryRow(ctx, `SELECT count(*) FROM users WHERE password_hash NOT LIKE '$argon2id$%'`).Scan(&bad))
	require.Zero(t, bad)

	login := newSeedLogin(t, db)
	for _, u := range d.Users {
		out, err := login.Execute(ctx, auth.LoginInput{Email: u.Email, Password: u.Password})
		if u.Status == "inactive" {
			require.Error(t, err, "inactive user %s must not log in", u.Email)
			code, ok := apperr.CodeOf(err)
			require.True(t, ok)
			require.Equal(t, apperr.InvalidCredentials, code)
			continue
		}
		require.NoError(t, err, "demo user %s must log in", u.Email)
		require.NotEmpty(t, out.AccessToken)
	}
	_, err = login.Execute(ctx, auth.LoginInput{Email: d.Users[0].Email, Password: "not-the-demo-password"})
	require.Error(t, err)
}

func newSeedLogin(t *testing.T, db *testkit.DB) *auth.LoginUseCase {
	t.Helper()
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k1.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	cfg := seedHasherConfig(dir)
	clk := seedClock{}
	jwt, err := authn.NewJWT(cfg, clk)
	require.NoError(t, err)
	hasher, err := authn.NewHasher(cfg)
	require.NoError(t, err)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	uc, err := auth.NewLoginUseCase(auth.Deps{
		Config: auth.Config{RefreshTTL: time.Hour},
		Tx:     postgres.NewTxRunner(db.App, log), Users: postgres.NewAuthUserRepo(db.App, log),
		Refresh: postgres.NewRefreshTokenRepo(db.App, log), Codec: authn.NewRefreshCodec(),
		Issuer: jwt, Verify: jwt, Hasher: hasher, Clock: clk, IDs: seedIDs{}, Log: log,
	})
	require.NoError(t, err)
	return uc
}

type seedClock struct{}

func (seedClock) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

type seedIDs struct{}

func (seedIDs) New() uuid.UUID { return uuid.Must(uuid.NewV7()) }
