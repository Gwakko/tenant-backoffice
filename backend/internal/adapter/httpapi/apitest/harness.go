//go:build integration

package apitest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

// Password is the password of every user of the World in a Stack.
const Password = "correct-horse-battery-1"

// Stack is the real API (app.BuildAPIWith: the real use cases, repositories,
// transactions and middleware) over a throwaway database cloned from the
// migrated template, connected as tb_app, with the isolation World written
// into it and every World user able to sign in with Password.
type Stack struct {
	T      *testing.T
	DB     *testkit.DB
	World  *isolation.World
	Server *httptest.Server
	// Anon is a client without a token.
	Anon *Client
	// Log receives everything the API logs.
	Log    *LogBuffer
	Config config.Config
	api    *app.API
}

// LogBuffer is a concurrency-safe log sink.
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String is everything logged so far.
func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// StackOption changes the configuration of a Stack.
type StackOption func(*config.Config)

// WithRate sets the request budgets. Without it the budgets are high, so that
// tests that sign in many times do not meet the limiter by accident.
func WithRate(r config.RateLimit) StackOption { return func(c *config.Config) { c.Rate = r } }

// WithDefaultRates uses the budgets of the spec (login 5 per pair and minute).
func WithDefaultRates() StackOption {
	return WithRate(config.RateLimit{
		LoginPairPerMin: 5, LoginIPPerMin: 20, RefreshIPPerMin: 20, MutationPerMin: 60, ReadPerMin: 600, MaxKeys: 10000,
	})
}

// WithCookieSecure sets TB_COOKIE_SECURE.
func WithCookieSecure(secure bool) StackOption {
	return func(c *config.Config) { c.Auth.CookieSecure = secure }
}

// NewStack builds the API over a new database and a new World. Everything is
// closed when the test ends.
func NewStack(t *testing.T, opts ...StackOption) *Stack {
	t.Helper()
	ctx := context.Background()
	db := testkit.NewDB(t)
	world := isolation.NewWorld(ctx, t, db)

	cfg := stackConfig(t, db)
	for _, o := range opts {
		o(&cfg)
	}
	setPasswords(ctx, t, db, world, cfg)

	logs := &LogBuffer{}
	api, err := app.BuildAPIWith(cfg, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	require.NoError(t, err)
	t.Cleanup(api.Close)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &Stack{T: t, DB: db, World: world, Server: srv, Anon: New(t, srv.URL), Log: logs, Config: cfg, api: api}
}

// As signs the World user in with Password and returns a client with the token.
func (s *Stack) As(user uuid.UUID) *Client {
	s.T.Helper()
	return s.Anon.Login(s.World.Emails[user], Password)
}

// Login signs in and returns the raw answer (cookie and body), for tests of the
// login itself.
func (s *Stack) Login(email, password string) *Response {
	s.T.Helper()
	return s.Anon.Post("/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

// SigningKeyDir returns the directory of the test signing key.
func (s *Stack) SigningKeyDir() string { return s.Config.Auth.JWTKeysDir }

func stackConfig(t *testing.T, db *testkit.DB) config.Config {
	t.Helper()
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k1.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	cursorKey := make([]byte, 32)
	_, err = rand.Read(cursorKey)
	require.NoError(t, err)

	return config.Config{
		HTTPAddr: "127.0.0.1:0", LogLevel: "debug", LogFormat: "json",
		DB: config.Database{
			URL: db.AppURL(t), MaxConns: 8,
			StatementTimeout: 10 * time.Second, IdleInTxTimeout: 10 * time.Second, ConnectTimeout: 10 * time.Second,
		},
		Auth: config.Auth{
			JWTKeysDir: dir, JWTActiveKID: "k1", JWTIssuer: "tenant-backoffice", JWTAudience: "tenant-backoffice-api",
			JWTLeeway: 30 * time.Second, AccessTTL: 10 * time.Minute, RefreshTTL: 168 * time.Hour, CookieSecure: true,
			// Cheap argon2id: the tests care about the flow, not the cost.
			Argon2MemoryKiB: 64, Argon2Time: 1, Argon2Threads: 1, Argon2KeyLen: 32, Argon2SaltLen: 16,
		},
		HTTP: config.HTTP{
			MaxBodyBytes: 65536, CursorKey: base64.StdEncoding.EncodeToString(cursorKey), CursorTTL: time.Hour,
		},
		Rate: config.RateLimit{
			LoginPairPerMin: 100000, LoginIPPerMin: 100000, RefreshIPPerMin: 100000,
			MutationPerMin: 100000, ReadPerMin: 100000, MaxKeys: 10000,
		},
	}
}

// setPasswords gives every World user a real hash of Password.
func setPasswords(ctx context.Context, t *testing.T, db *testkit.DB, w *isolation.World, cfg config.Config) {
	t.Helper()
	hasher, err := app.PasswordHasher(cfg)
	require.NoError(t, err)
	hash, err := hasher.Hash(Password)
	require.NoError(t, err)
	for _, id := range w.AllUsers {
		_, err := db.Owner.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, string(hash))
		require.NoError(t, err)
	}
}
