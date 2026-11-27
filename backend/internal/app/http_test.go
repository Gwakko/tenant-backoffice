package app_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// users is an AuthUserRepo that serves ByID only.
type users struct {
	port.AuthUserRepo
	mu    sync.Mutex
	rec   map[user.ID]port.AuthUser
	fail  error
	calls int
}

func (u *users) ByID(_ context.Context, id user.ID) (port.AuthUser, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	if u.fail != nil {
		return port.AuthUser{}, false, u.fail
	}
	r, ok := u.rec[id]
	return r, ok, nil
}

func (u *users) set(r port.AuthUser) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rec[r.ID] = r
}

type stack struct {
	h     http.Handler
	jwt   *authn.JWT
	users *users
	clk   *clock
	log   *bytes.Buffer
	dir   string
	priv  ed25519.PrivateKey
}

func writeKey(t *testing.T, dir, kid string) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, kid+".pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	return priv
}

// newStack wires the real server with the real JWT adapter, the real use case
// of authentication over an in-memory user table, and the real limiter.
func newStack(t *testing.T) *stack {
	t.Helper()
	dir := t.TempDir()
	priv := writeKey(t, dir, "k1")
	clk := &clock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	t.Setenv("TB_HTTP_ADDR", ":0")
	t.Setenv("TB_JWT_KEYS_DIR", dir)
	t.Setenv("TB_JWT_ACTIVE_KID", "k1")
	cfg, err := config.Load()
	require.NoError(t, err)

	j, err := authn.NewJWT(app.AuthnConfig(cfg), clk)
	require.NoError(t, err)
	us := &users{rec: map[user.ID]port.AuthUser{}}
	uc, err := auth.NewAuthenticateUseCase(auth.Deps{Users: us, Verify: j})
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(&lockedWriter{w: buf}, nil))
	opts := app.HTTPOptions(cfg, log, clk, uc)
	opts.Health = httpapi.NewHealthHandler(func(context.Context) error { return nil })
	h, err := httpapi.NewServer(opts)
	require.NoError(t, err)
	return &stack{h: h, jwt: j, users: us, clk: clk, log: buf, dir: dir, priv: priv}
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (s *stack) call(t *testing.T, method, path, body, authz string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	r := httptest.NewRequestWithContext(context.Background(), method, path, rd)
	r.RemoteAddr = "192.0.2.77:4000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p httpapi.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p.Code
}

func (s *stack) token(t *testing.T, id user.ID, tv int64) string {
	t.Helper()
	at, err := s.jwt.Issue(id, tv)
	require.NoError(t, err)
	return "Bearer " + at.Token
}

func newUser(tv int64, status user.Status) port.AuthUser {
	return port.AuthUser{ID: user.ID(uuid.New()), Status: status, TokenVersion: tv}
}

func TestValidTokenReachesPlaceholder(t *testing.T) {
	s := newStack(t)
	u := newUser(3, user.StatusActive)
	s.users.set(u)
	rec := s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, u.ID, 3))
	require.Equal(t, http.StatusNotImplemented, rec.Code, "authenticated: the placeholder answers")
}

// Test-ID: T-U-68
func TestStaleTokenVersionIsRejected(t *testing.T) {
	s := newStack(t)
	u := newUser(2, user.StatusActive)
	s.users.set(u)
	old := s.token(t, u.ID, 1) // issued before a password change bumped the version
	rec := s.call(t, http.MethodGet, "/api/v1/me", "", old)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "token_invalid", codeOf(t, rec))

	// A token of a newer version than the database is not valid either.
	require.Equal(t, "token_invalid", codeOf(t, s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, u.ID, 3))))
	// The current version works.
	require.Equal(t, http.StatusNotImplemented, s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, u.ID, 2)).Code)

	// The version is read from the database on every request.
	u.TokenVersion = 3
	s.users.set(u)
	require.Equal(t, "token_invalid", codeOf(t, s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, u.ID, 2))))
}

func TestInactiveAndUnknownUserAreRejected(t *testing.T) {
	s := newStack(t)
	inactive := newUser(1, user.StatusInactive)
	s.users.set(inactive)
	require.Equal(t, "token_invalid", codeOf(t, s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, inactive.ID, 1))))
	unknown := user.ID(uuid.New())
	require.Equal(t, "token_invalid", codeOf(t, s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, unknown, 1))))
}

func TestExpiredToken(t *testing.T) {
	s := newStack(t)
	u := newUser(1, user.StatusActive)
	s.users.set(u)
	tok := s.token(t, u.ID, 1)
	s.clk.Advance(10*time.Minute + 29*time.Second)
	require.Equal(t, http.StatusNotImplemented, s.call(t, http.MethodGet, "/api/v1/me", "", tok).Code, "inside the leeway")
	s.clk.Advance(2 * time.Second)
	rec := s.call(t, http.MethodGet, "/api/v1/me", "", tok)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "token_expired", codeOf(t, rec))
}

func b64(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestForgedAlgorithmsAreRejected(t *testing.T) {
	s := newStack(t)
	u := newUser(1, user.StatusActive)
	s.users.set(u)
	now := s.clk.Now()
	payload := map[string]any{
		"sub": u.ID.String(), "tv": 1, "iss": "tenant-backoffice", "aud": "tenant-backoffice-api",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": uuid.NewString(),
	}
	pub := s.priv.Public().(ed25519.PublicKey)

	for name, tok := range map[string]string{
		"alg none":          b64(map[string]any{"alg": "none", "typ": "JWT", "kid": "k1"}) + "." + b64(payload) + ".",
		"alg None":          b64(map[string]any{"alg": "None", "typ": "JWT", "kid": "k1"}) + "." + b64(payload) + ".",
		"alg none, garbage": b64(map[string]any{"alg": "none", "kid": "k1"}) + "." + b64(payload) + ".AAAA",
		"HS256 by pub key":  b64(map[string]any{"alg": "HS256", "kid": "k1"}) + "." + b64(payload) + "." + b64(string(pub)),
		"EdDSA bad sig":     b64(map[string]any{"alg": "EdDSA", "kid": "k1"}) + "." + b64(payload) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 64)),
		"EdDSA unknown kid": b64(map[string]any{"alg": "EdDSA", "kid": "nope"}) + "." + b64(payload) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 64)),
		"not a jwt":         "abc",
	} {
		rec := s.call(t, http.MethodGet, "/api/v1/me", "", "Bearer "+tok)
		require.Equal(t, http.StatusUnauthorized, rec.Code, name)
		require.Equal(t, "token_invalid", codeOf(t, rec), name)
	}
	require.Zero(t, s.users.calls, "a token that fails verification never reaches the database")
}

func TestStoreFailureIsNotLeaked(t *testing.T) {
	s := newStack(t)
	u := newUser(1, user.StatusActive)
	s.users.set(u)
	s.users.fail = errors.New("pq: connection refused host=10.0.0.5 password=s3cr3t")
	rec := s.call(t, http.MethodGet, "/api/v1/me", "", s.token(t, u.ID, 1))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "internal", codeOf(t, rec))
	require.NotContains(t, rec.Body.String(), "s3cr3t")
	require.NotContains(t, rec.Body.String(), "10.0.0.5")
}

func TestLoginRateLimitTripsAndResetsByClock(t *testing.T) {
	s := newStack(t)
	login := func(email string) *httptest.ResponseRecorder {
		return s.call(t, http.MethodPost, "/api/v1/auth/login", `{"email":"`+email+`","password":"wrong"}`, "")
	}
	for i := 1; i <= 5; i++ {
		require.Equal(t, http.StatusNotImplemented, login("Victim@Example.com").Code, "attempt %d", i)
	}
	rec := login(" victim@example.COM")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "6th attempt for the same (address, email) pair (EC-108)")
	require.Equal(t, "rate_limited", codeOf(t, rec))
	require.Equal(t, "12", rec.Header().Get("Retry-After"))

	require.Equal(t, http.StatusNotImplemented, login("someone-else@example.com").Code, "another email is another bucket")

	s.clk.Advance(11 * time.Second)
	require.Equal(t, http.StatusTooManyRequests, login("victim@example.com").Code)
	s.clk.Advance(2 * time.Second)
	require.Equal(t, http.StatusNotImplemented, login("victim@example.com").Code, "reset by the clock")
}

func TestLoginRateLimitPerAddress(t *testing.T) {
	s := newStack(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 21; i++ {
		last = s.call(t, http.MethodPost, "/api/v1/auth/login",
			`{"email":"user`+string(rune('a'+i))+`@example.com","password":"x"}`, "")
	}
	require.Equal(t, http.StatusTooManyRequests, last.Code, "21st login from one address in a minute")
}

func TestMutationRateLimitPerUser(t *testing.T) {
	s := newStack(t)
	u1, u2 := newUser(1, user.StatusActive), newUser(1, user.StatusActive)
	s.users.set(u1)
	s.users.set(u2)
	t1, t2 := s.token(t, u1.ID, 1), s.token(t, u2.ID, 1)
	path := "/api/v1/nodes/" + uuid.NewString() + "/deactivate"
	for i := 1; i <= 60; i++ {
		require.Equal(t, http.StatusNotImplemented, s.call(t, http.MethodPost, path, "", t1).Code, "mutation %d", i)
	}
	rec := s.call(t, http.MethodPost, path, "", t1)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "61st mutation in a minute (EC-116)")
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
	require.Equal(t, http.StatusNotImplemented, s.call(t, http.MethodGet, "/api/v1/me", "", t1).Code, "reads have their own bucket")
	require.Equal(t, http.StatusNotImplemented, s.call(t, http.MethodPost, path, "", t2).Code, "another user is not affected")
	s.clk.Advance(2 * time.Second)
	require.Equal(t, http.StatusNotImplemented, s.call(t, http.MethodPost, path, "", t1).Code, "one token refilled per second")
}

func TestAccessLogOfRealStackHasNoSecrets(t *testing.T) {
	s := newStack(t)
	u := newUser(1, user.StatusActive)
	s.users.set(u)
	tok := s.token(t, u.ID, 1)
	s.call(t, http.MethodGet, "/api/v1/me", "", tok)
	s.call(t, http.MethodPost, "/api/v1/auth/login", `{"email":"x@example.com","password":"Sup3rSecretPassw0rd"}`, "")
	out := s.log.String()
	require.NotEmpty(t, out)
	require.NotContains(t, out, strings.TrimPrefix(tok, "Bearer "))
	require.NotContains(t, out, "Sup3rSecretPassw0rd")
	require.NotContains(t, strings.ToLower(out), "authorization")
	require.NotContains(t, out, "Bearer")
}

func TestCursorCodecFromConfig(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	clk := &clock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))

	cfg, err := config.Load()
	require.NoError(t, err)
	c, err := app.CursorCodec(cfg, clk, log)
	require.NoError(t, err)
	require.Contains(t, logBuf.String(), "TB_CURSOR_KEY is not set")
	bind := httpapi.CursorBinding{Scope: "s"}
	tok, err := c.Encode([]string{"a"}, bind)
	require.NoError(t, err)
	c2, err := app.CursorCodec(cfg, clk, log)
	require.NoError(t, err)
	_, err = c2.Decode(tok, bind)
	require.Error(t, err, "an ephemeral key is per codec: another process cannot verify the cursor")

	t.Setenv("TB_CURSOR_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	cfg, err = config.Load()
	require.NoError(t, err)
	a, err := app.CursorCodec(cfg, clk, log)
	require.NoError(t, err)
	b, err := app.CursorCodec(cfg, clk, log)
	require.NoError(t, err)
	tok, err = a.Encode([]string{"a"}, bind)
	require.NoError(t, err)
	_, err = b.Decode(tok, bind)
	require.NoError(t, err, "the same configured key verifies across processes")
}

func TestBuildAPIWiresTheServer(t *testing.T) {
	t.Setenv("TB_HTTP_ADDR", ":0")
	cfg, err := config.Load()
	require.NoError(t, err)
	_, err = app.BuildAPI(cfg)
	require.NoError(t, err)
}
