//go:build integration

package postgres_test

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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const authPassword = "correct horse battery"

type authClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *authClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *authClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type authIDs struct{}

func (authIDs) New() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// authWorld is the real authentication stack (argon2id, EdDSA, repositories on
// tb_app, real TxRunner) over a throwaway database.
type authWorld struct {
	t      *testing.T
	db     *testkit.DB
	clock  *authClock
	hasher *authn.Hasher
	users  *postgres.AuthUserRepo
	tokens *postgres.RefreshTokenRepo
	deps   auth.Deps

	login        *auth.LoginUseCase
	refresh      *auth.RefreshUseCase
	logout       *auth.LogoutUseCase
	changePasswd *auth.ChangePasswordUseCase
	authenticate *auth.AuthenticateUseCase
	runner       *postgres.TxRunner
}

func newAuthWorld(t *testing.T) *authWorld {
	t.Helper()
	db := testkit.NewDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A key generated for this test only, in a temporary directory.
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k1.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))

	cfg := authn.Config{
		KeysDir: dir, ActiveKID: "k1", Issuer: "tenant-backoffice", Audience: "tenant-backoffice-api",
		AccessTTL: 10 * time.Minute, Leeway: 30 * time.Second,
		Argon2MemoryKiB: 16, Argon2Time: 1, Argon2Threads: 1, Argon2KeyLen: 32, Argon2SaltLen: 16,
	}
	clock := &authClock{t: time.Now().UTC().Truncate(time.Microsecond)}
	jwt, err := authn.NewJWT(cfg, clock)
	require.NoError(t, err)
	hasher, err := authn.NewHasher(cfg)
	require.NoError(t, err)

	w := &authWorld{t: t, db: db, clock: clock, hasher: hasher}
	w.runner = postgres.NewTxRunner(db.App, log)
	w.users = postgres.NewAuthUserRepo(db.App, log)
	w.tokens = postgres.NewRefreshTokenRepo(db.App, log)
	w.deps = auth.Deps{
		Config: auth.Config{RefreshTTL: 7 * 24 * time.Hour},
		Tx:     w.runner, Users: w.users, Profile: w.users, Refresh: w.tokens, Codec: authn.NewRefreshCodec(),
		Issuer: jwt, Verify: jwt, Hasher: hasher, Clock: clock, IDs: authIDs{}, Log: log,
	}
	w.login, err = auth.NewLoginUseCase(w.deps)
	require.NoError(t, err)
	w.refresh, err = auth.NewRefreshUseCase(w.deps)
	require.NoError(t, err)
	w.logout, err = auth.NewLogoutUseCase(w.deps)
	require.NoError(t, err)
	w.changePasswd, err = auth.NewChangePasswordUseCase(w.deps)
	require.NoError(t, err)
	w.authenticate, err = auth.NewAuthenticateUseCase(w.deps)
	require.NoError(t, err)
	return w
}

func (w *authWorld) addUser(email, password, status string) user.ID {
	w.t.Helper()
	hash, err := w.hasher.Hash(password)
	require.NoError(w.t, err)
	id := uuid.Must(uuid.NewV7())
	_, err = w.db.Owner.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash, status, created_at) VALUES ($1, $2, $3, $4, now())`,
		id, email, string(hash), status)
	require.NoError(w.t, err)
	return user.ID(id)
}

func (w *authWorld) doLogin(email, password string) (auth.LoginOutput, error) {
	return w.login.Execute(context.Background(), auth.LoginInput{Email: email, Password: password})
}

// revoked counts the refresh tokens of the user with and without revoked_at.
func (w *authWorld) revoked(id user.ID) (revoked, live int) {
	w.t.Helper()
	err := w.db.Owner.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE revoked_at IS NOT NULL), count(*) FILTER (WHERE revoked_at IS NULL)
		   FROM refresh_tokens WHERE user_id = $1`, uuid.UUID(id)).Scan(&revoked, &live)
	require.NoError(w.t, err)
	return revoked, live
}

func (w *authWorld) tokenVersion(id user.ID) int64 {
	w.t.Helper()
	var tv int64
	require.NoError(w.t, w.db.Owner.QueryRow(context.Background(),
		`SELECT token_version FROM users WHERE id = $1`, uuid.UUID(id)).Scan(&tv))
	return tv
}

func requireAuthCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	require.Error(t, err)
	code, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, want, code)
}

// Test-ID: T-I-41
func TestRefreshReuseRevokesFamily(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	id := w.addUser("ann@example.test", authPassword, "active")

	first, err := w.doLogin("ann@example.test", authPassword)
	require.NoError(t, err)
	second, err := w.doLogin("ann@example.test", authPassword) // another session, another family
	require.NoError(t, err)

	r2, err := w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: first.RefreshToken})
	require.NoError(t, err)
	r3, err := w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: r2.RefreshToken})
	require.NoError(t, err)

	// R1 again: a replay. The family is revoked and the call fails.
	_, err = w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: first.RefreshToken})
	requireAuthCode(t, err, apperr.RefreshInvalid)
	revoked, live := w.revoked(id)
	require.Equal(t, 3, revoked, "the three tokens of the replayed family")
	require.Equal(t, 1, live, "the other session is untouched")

	// The newest token of the family is dead as well.
	_, err = w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: r3.RefreshToken})
	requireAuthCode(t, err, apperr.RefreshInvalid)
	// The other family still works.
	_, err = w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: second.RefreshToken})
	require.NoError(t, err)

	t.Run("tokens are stored as hashes", func(t *testing.T) {
		var n int
		require.NoError(t, w.db.Owner.QueryRow(ctx,
			`SELECT count(*) FROM refresh_tokens WHERE token_hash = convert_to($1, 'UTF8')`, first.RefreshToken).Scan(&n))
		require.Zero(t, n)
	})
}

// Test-ID: T-I-51
func TestRefreshConcurrentUseOfOneToken(t *testing.T) {
	w := newAuthWorld(t)
	id := w.addUser("ann@example.test", authPassword, "active")
	sess, err := w.doLogin("ann@example.test", authPassword)
	require.NoError(t, err)

	const racers = 6
	results := make(chan error, racers)
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.refresh.Execute(context.Background(), auth.RefreshInput{RefreshToken: sess.RefreshToken})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
			continue
		}
		requireAuthCode(t, err, apperr.RefreshInvalid)
	}
	require.Equal(t, 1, ok, "a one-time token succeeds exactly once")
	_, live := w.revoked(id)
	require.Zero(t, live, "the losers saw a replay and revoked the family")
}

func TestRefreshExpiryAndLogout(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	id := w.addUser("ann@example.test", authPassword, "active")

	a, err := w.doLogin("ann@example.test", authPassword)
	require.NoError(t, err)
	b, err := w.doLogin("ann@example.test", authPassword)
	require.NoError(t, err)

	require.NoError(t, w.logout.Execute(ctx, auth.LogoutInput{RefreshToken: a.RefreshToken}))
	_, err = w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: a.RefreshToken})
	requireAuthCode(t, err, apperr.RefreshInvalid)
	_, live := w.revoked(id)
	require.Equal(t, 1, live)

	w.clock.Advance(7*24*time.Hour + time.Second)
	_, err = w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: b.RefreshToken})
	requireAuthCode(t, err, apperr.RefreshInvalid)
}

func TestLoginAndAuthenticateOnDatabase(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	id := w.addUser("ann@example.test", authPassword, "active")
	w.addUser("off@example.test", authPassword, "inactive")

	out, err := w.doLogin("Ann@Example.test", authPassword)
	require.NoError(t, err)
	got, err := w.authenticate.Execute(ctx, auth.AuthenticateInput{AccessToken: out.AccessToken})
	require.NoError(t, err)
	require.Equal(t, id, got.Actor)

	for _, c := range [][2]string{{"ann@example.test", "wrong password!!"}, {"nobody@example.test", authPassword}, {"off@example.test", authPassword}} {
		_, err := w.doLogin(c[0], c[1])
		requireAuthCode(t, err, apperr.InvalidCredentials)
	}

	// Deactivation (token_version is not bumped here) still stops the token.
	_, err = w.db.Owner.Exec(ctx, `UPDATE users SET status = 'inactive' WHERE id = $1`, uuid.UUID(id))
	require.NoError(t, err)
	_, err = w.authenticate.Execute(ctx, auth.AuthenticateInput{AccessToken: out.AccessToken})
	requireAuthCode(t, err, apperr.TokenInvalid)
}

// Test-ID: T-I-52
func TestChangePasswordOnDatabase(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	id := w.addUser("ann@example.test", authPassword, "active")
	other := w.addUser("bob@example.test", authPassword, "active")

	s1, err := w.doLogin("ann@example.test", authPassword)
	require.NoError(t, err)
	_, err = w.doLogin("ann@example.test", authPassword)
	require.NoError(t, err)
	bob, err := w.doLogin("bob@example.test", authPassword)
	require.NoError(t, err)

	const newPassword = "another long passphrase"
	require.NoError(t, w.changePasswd.Execute(ctx, auth.ChangePasswordInput{Actor: id, CurrentPassword: authPassword, NewPassword: newPassword}))

	require.EqualValues(t, 1, w.tokenVersion(id))
	revoked, live := w.revoked(id)
	require.Equal(t, 2, revoked)
	require.Zero(t, live)
	_, bobLive := w.revoked(other)
	require.Equal(t, 1, bobLive, "another user's sessions are untouched")
	require.Zero(t, w.tokenVersion(other))

	_, err = w.authenticate.Execute(ctx, auth.AuthenticateInput{AccessToken: s1.AccessToken})
	requireAuthCode(t, err, apperr.TokenInvalid)
	_, err = w.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: s1.RefreshToken})
	requireAuthCode(t, err, apperr.RefreshInvalid)
	_, err = w.authenticate.Execute(ctx, auth.AuthenticateInput{AccessToken: bob.AccessToken})
	require.NoError(t, err)

	_, err = w.doLogin("ann@example.test", authPassword)
	requireAuthCode(t, err, apperr.InvalidCredentials)
	fresh, err := w.doLogin("ann@example.test", newPassword)
	require.NoError(t, err)
	_, err = w.authenticate.Execute(ctx, auth.AuthenticateInput{AccessToken: fresh.AccessToken})
	require.NoError(t, err)

	t.Run("a wrong current password changes nothing", func(t *testing.T) {
		err := w.changePasswd.Execute(ctx, auth.ChangePasswordInput{Actor: id, CurrentPassword: "not the password", NewPassword: "yet another passphrase"})
		requireAuthCode(t, err, apperr.InvalidCredentials)
		require.EqualValues(t, 1, w.tokenVersion(id))
		_, err = w.doLogin("ann@example.test", newPassword)
		require.NoError(t, err)
	})
	t.Run("a failed commit leaves hash, version and tokens as they were", func(t *testing.T) {
		before := w.tokenVersion(id)
		_, liveBefore := w.revoked(id)
		w.runner.BeforeCommit(func(context.Context, pgx.Tx) error { return context.DeadlineExceeded })
		err := w.changePasswd.Execute(ctx, auth.ChangePasswordInput{Actor: id, CurrentPassword: newPassword, NewPassword: "a third long passphrase"})
		w.runner.BeforeCommit(nil)
		require.Error(t, err)
		require.Equal(t, before, w.tokenVersion(id))
		_, liveAfter := w.revoked(id)
		require.Equal(t, liveBefore, liveAfter)
		_, err = w.doLogin("ann@example.test", newPassword)
		require.NoError(t, err, "the old password still works")
	})
}

func TestAuthUserRepoVisibility(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	id := w.addUser("ann@example.test", authPassword, "active")
	other := w.addUser("bob@example.test", authPassword, "active")

	u, found, err := w.users.ByEmail(ctx, "ann@example.test")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, u.ID)
	require.NotEmpty(t, u.PasswordHash)

	u, found, err = w.users.ByID(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, u.PasswordHash, "tb_auth_user_by_id returns no hash")

	_, found, err = w.users.ByEmail(ctx, "nobody@example.test")
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = w.users.ByID(ctx, user.ID(uuid.New()))
	require.NoError(t, err)
	require.False(t, found)

	t.Run("a hash is readable only for the actor itself", func(t *testing.T) {
		err := w.runner.InTx(ctx, id, port.TxOptions{}, func(ctx context.Context, tx port.Tx) error {
			h, err := w.users.PasswordHash(ctx, tx, id)
			require.NoError(t, err)
			require.NotEmpty(t, h)
			_, err = w.users.PasswordHash(ctx, tx, other)
			requireAuthCode(t, err, apperr.NotFound)
			return nil
		})
		require.NoError(t, err)
	})
	t.Run("a foreign transaction type is rejected", func(t *testing.T) {
		type fakeTx struct{ port.Tx }
		_, err := w.users.PasswordHash(ctx, fakeTx{}, id)
		require.Error(t, err)
	})
}
