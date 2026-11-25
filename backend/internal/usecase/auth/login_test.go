package auth_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

func (w *world) login(t *testing.T) *auth.LoginUseCase {
	uc, err := auth.NewLoginUseCase(w.deps)
	require.NoError(t, err)
	return uc
}

func TestLoginSuccess(t *testing.T) {
	w := newWorld(t)
	u := w.store.addUser("ann@example.test", goodPassword, user.StatusActive)
	u.tv = 4
	var logs bytes.Buffer
	w.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	uc := w.login(t)

	out, err := uc.Execute(t.Context(), auth.LoginInput{Email: "  Ann@Example.test ", Password: goodPassword, IP: "192.0.2.1"})
	require.NoError(t, err)
	require.Equal(t, u.id, out.UserID)
	require.Equal(t, user.Email("ann@example.test"), out.Email)
	require.Equal(t, "at:"+u.id.String()+":4", out.AccessToken)
	require.Equal(t, 10*60, int(out.ExpiresIn.Seconds()))
	require.Len(t, out.RefreshToken, 43)
	require.Equal(t, w.clock.Now().Add(7*24*60*60*1e9), out.RefreshExpiresAt)

	require.Len(t, w.store.tokens, 1)
	require.Equal(t, []byte(out.RefreshToken), w.store.tokens[0].Hash, "the stored value is the hash, not the plain token text")
	require.Equal(t, []user.ActorID{u.id}, w.store.actors, "the transaction runs as the user that was just identified")

	require.Contains(t, logs.String(), u.id.String())
	for _, secret := range []string{goodPassword, out.RefreshToken, out.AccessToken, "h:" + goodPassword} {
		require.NotContains(t, logs.String(), secret, "secrets must not be logged")
	}
}

func TestLoginNewFamilyPerLogin(t *testing.T) {
	w := newWorld(t)
	w.store.addUser("ann@example.test", goodPassword, user.StatusActive)
	uc := w.login(t)
	in := auth.LoginInput{Email: "ann@example.test", Password: goodPassword}
	_, err := uc.Execute(t.Context(), in)
	require.NoError(t, err)
	_, err = uc.Execute(t.Context(), in)
	require.NoError(t, err)
	require.NotEqual(t, w.store.tokens[0].FamilyID, w.store.tokens[1].FamilyID)
}

// Test-ID: T-U-33
func TestUniformFailure(t *testing.T) {
	w := newWorld(t)
	w.store.addUser("ann@example.test", goodPassword, user.StatusActive)
	w.store.addUser("off@example.test", goodPassword, user.StatusInactive)
	var logs bytes.Buffer
	w.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	uc := w.login(t)
	w.hasher.verifies = nil // the constructor hashed the dummy once; start counting from here

	cases := []struct {
		name string
		in   auth.LoginInput
	}{
		{"wrong password", auth.LoginInput{Email: "ann@example.test", Password: "wrong password!!"}},
		{"unknown email", auth.LoginInput{Email: "nobody@example.test", Password: goodPassword}},
		{"inactive account with the right password", auth.LoginInput{Email: "off@example.test", Password: goodPassword}},
	}
	var errs []error
	for _, c := range cases {
		before := len(w.hasher.verifies)
		_, err := uc.Execute(t.Context(), c.in)
		requireCode(t, err, apperr.InvalidCredentials)
		require.Equal(t, before+1, len(w.hasher.verifies), "%s: exactly one hash verification", c.name)
		errs = append(errs, err)
	}
	require.Equal(t, errs[0], errs[1], "identical error value for a wrong password and an unknown email")
	require.Equal(t, errs[0].Error(), errs[2].Error())
	require.Empty(t, w.store.tokens, "no session was opened")
	require.Empty(t, w.store.actors, "no transaction was opened")

	// The unknown email was verified against a dummy hash, not skipped.
	dummy := w.hasher.verifies[1]
	require.NotEmpty(t, dummy)
	require.NotEqual(t, w.hasher.verifies[0], dummy)
	for _, c := range cases {
		require.NotContains(t, logs.String(), c.in.Password)
	}
	require.NotContains(t, logs.String(), "nobody@example.test", "an attempted email is not logged")
}

func TestLoginMalformedCredentials(t *testing.T) {
	w := newWorld(t)
	w.store.addUser("ann@example.test", goodPassword, user.StatusActive)
	uc := w.login(t)
	w.hasher.verifies = nil
	for _, in := range []auth.LoginInput{
		{Email: "", Password: goodPassword},
		{Email: "ann@example.test", Password: ""},
		{Email: "ann@example.test", Password: strings.Repeat("a", 129)},
		{Email: "not-an-email", Password: goodPassword},
	} {
		_, err := uc.Execute(t.Context(), in)
		requireCode(t, err, apperr.InvalidCredentials)
	}
	require.Empty(t, w.hasher.verifies, "nothing is hashed for input that can never be valid")
}

func TestLoginRollsBackOnStoreFailure(t *testing.T) {
	w := newWorld(t)
	w.store.addUser("ann@example.test", goodPassword, user.StatusActive)
	uc := w.login(t)
	w.store.failOn = "insert"
	_, err := uc.Execute(t.Context(), auth.LoginInput{Email: "ann@example.test", Password: goodPassword})
	require.Error(t, err)
	require.Empty(t, w.store.tokens)
}

func TestConstructorsRequireDependencies(t *testing.T) {
	_, err := auth.NewLoginUseCase(auth.Deps{})
	require.Error(t, err)
	_, err = auth.NewRefreshUseCase(auth.Deps{})
	require.Error(t, err)
	_, err = auth.NewLogoutUseCase(auth.Deps{})
	require.Error(t, err)
	_, err = auth.NewChangePasswordUseCase(auth.Deps{})
	require.Error(t, err)
	_, err = auth.NewAuthenticateUseCase(auth.Deps{})
	require.Error(t, err)
}
