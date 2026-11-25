package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

type sessionWorld struct {
	*world
	u       *userRec
	refresh *auth.RefreshUseCase
	logout  *auth.LogoutUseCase
	change  *auth.ChangePasswordUseCase
	authn   *auth.AuthenticateUseCase
	first   auth.LoginOutput
}

func newSessionWorld(ctx context.Context, t *testing.T) *sessionWorld {
	w := newWorld(t)
	u := w.store.addUser("ann@example.test", goodPassword, user.StatusActive)
	s := &sessionWorld{world: w, u: u}
	var err error
	login := w.login(t)
	s.refresh, err = auth.NewRefreshUseCase(w.deps)
	require.NoError(t, err)
	s.logout, err = auth.NewLogoutUseCase(w.deps)
	require.NoError(t, err)
	s.change, err = auth.NewChangePasswordUseCase(w.deps)
	require.NoError(t, err)
	s.authn, err = auth.NewAuthenticateUseCase(w.deps)
	require.NoError(t, err)
	s.first, err = login.Execute(ctx, auth.LoginInput{Email: "ann@example.test", Password: goodPassword})
	require.NoError(t, err)
	return s
}

func (s *sessionWorld) live() int {
	n := 0
	for _, t := range s.store.tokens {
		if t.RevokedAt == nil {
			n++
		}
	}
	return n
}

func TestRefreshRotates(t *testing.T) {
	ctx := t.Context()
	s := newSessionWorld(ctx, t)
	s.clock.Advance(time.Minute)
	out, err := s.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: s.first.RefreshToken})
	require.NoError(t, err)
	require.NotEqual(t, s.first.RefreshToken, out.RefreshToken)
	require.Equal(t, s.u.id, out.UserID)
	require.Len(t, s.store.tokens, 2)
	require.NotNil(t, s.store.tokens[0].UsedAt)
	require.Nil(t, s.store.tokens[1].UsedAt)
	require.Equal(t, s.store.tokens[0].FamilyID, s.store.tokens[1].FamilyID)
	require.Equal(t, s.clock.Now().Add(7*24*time.Hour), out.RefreshExpiresAt)
	require.Equal(t, s.u.id, s.store.actors[len(s.store.actors)-1])
}

// Test-ID: T-U-41
func TestRefreshReuseRevokesFamily(t *testing.T) {
	ctx := t.Context()
	s := newSessionWorld(ctx, t)
	r2, err := s.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: s.first.RefreshToken})
	require.NoError(t, err)

	_, err = s.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: s.first.RefreshToken})
	requireCode(t, err, apperr.RefreshInvalid)
	require.Zero(t, s.live(), "the revocation of the family is committed, not rolled back with the error")

	_, err = s.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: r2.RefreshToken})
	requireCode(t, err, apperr.RefreshInvalid)
}

func TestRefreshRejections(t *testing.T) {
	t.Run("malformed and unknown", func(t *testing.T) {
		s := newSessionWorld(t.Context(), t)
		for _, tok := range []string{"", "short", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!", "0000000000000000000000000000000000000000999"} {
			_, err := s.refresh.Execute(t.Context(), auth.RefreshInput{RefreshToken: tok})
			requireCode(t, err, apperr.RefreshInvalid)
		}
		require.Equal(t, 1, s.live())
	})
	t.Run("expired", func(t *testing.T) {
		s := newSessionWorld(t.Context(), t)
		s.clock.Advance(7*24*time.Hour + time.Second)
		_, err := s.refresh.Execute(t.Context(), auth.RefreshInput{RefreshToken: s.first.RefreshToken})
		requireCode(t, err, apperr.RefreshInvalid)
	})
	t.Run("deactivated user", func(t *testing.T) {
		s := newSessionWorld(t.Context(), t)
		s.u.status = user.StatusInactive
		_, err := s.refresh.Execute(t.Context(), auth.RefreshInput{RefreshToken: s.first.RefreshToken})
		requireCode(t, err, apperr.RefreshInvalid)
		require.Zero(t, s.live())
	})
	t.Run("revoked by logout", func(t *testing.T) {
		s := newSessionWorld(t.Context(), t)
		require.NoError(t, s.logout.Execute(t.Context(), auth.LogoutInput{RefreshToken: s.first.RefreshToken}))
		_, err := s.refresh.Execute(t.Context(), auth.RefreshInput{RefreshToken: s.first.RefreshToken})
		requireCode(t, err, apperr.RefreshInvalid)
	})
}

func TestLogout(t *testing.T) {
	ctx := t.Context()
	s := newSessionWorld(ctx, t)
	r2, err := s.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: s.first.RefreshToken})
	require.NoError(t, err)
	require.NoError(t, s.logout.Execute(ctx, auth.LogoutInput{RefreshToken: r2.RefreshToken}))
	require.Zero(t, s.live(), "the whole family is revoked")
	// Unknown, malformed and repeated tokens all succeed.
	for _, tok := range []string{"", "junk", "0000000000000000000000000000000000000000999", r2.RefreshToken} {
		require.NoError(t, s.logout.Execute(ctx, auth.LogoutInput{RefreshToken: tok}))
	}
}

func TestLogoutKeepsOtherSessions(t *testing.T) {
	s := newSessionWorld(t.Context(), t)
	other, err := s.login(t).Execute(t.Context(), auth.LoginInput{Email: "ann@example.test", Password: goodPassword})
	require.NoError(t, err)
	require.NoError(t, s.logout.Execute(t.Context(), auth.LogoutInput{RefreshToken: s.first.RefreshToken}))
	require.Equal(t, 1, s.live())
	_, err = s.refresh.Execute(t.Context(), auth.RefreshInput{RefreshToken: other.RefreshToken})
	require.NoError(t, err)
}

func TestChangePassword(t *testing.T) {
	ctx := t.Context()
	const newPassword = "another long passphrase"
	in := auth.ChangePasswordInput{Actor: user.ActorID{}, CurrentPassword: goodPassword, NewPassword: newPassword}

	t.Run("bumps token_version, revokes all families, replaces the hash", func(t *testing.T) {
		s := newSessionWorld(ctx, t)
		_, err := s.login(t).Execute(ctx, auth.LoginInput{Email: "ann@example.test", Password: goodPassword})
		require.NoError(t, err)
		require.Equal(t, 2, s.live())
		oldAccess := s.first.AccessToken

		in.Actor = s.u.id
		require.NoError(t, s.change.Execute(ctx, in))
		require.EqualValues(t, 1, s.u.tv)
		require.Zero(t, s.live())
		require.Equal(t, user.PasswordHash("h:"+newPassword), s.u.hash)

		_, err = s.authn.Execute(ctx, auth.AuthenticateInput{AccessToken: oldAccess})
		requireCode(t, err, apperr.TokenInvalid)
		_, err = s.refresh.Execute(ctx, auth.RefreshInput{RefreshToken: s.first.RefreshToken})
		requireCode(t, err, apperr.RefreshInvalid)
		_, err = s.login(t).Execute(ctx, auth.LoginInput{Email: "ann@example.test", Password: goodPassword})
		requireCode(t, err, apperr.InvalidCredentials)
		_, err = s.login(t).Execute(ctx, auth.LoginInput{Email: "ann@example.test", Password: newPassword})
		require.NoError(t, err)
	})
	t.Run("wrong current password changes nothing", func(t *testing.T) {
		s := newSessionWorld(ctx, t)
		bad := in
		bad.Actor, bad.CurrentPassword = s.u.id, "not the current one"
		requireCode(t, s.change.Execute(ctx, bad), apperr.InvalidCredentials)
		require.Zero(t, s.u.tv)
		require.Equal(t, 1, s.live())
	})
	t.Run("a failure in the middle rolls everything back", func(t *testing.T) {
		for _, op := range []string{"set-password", "bump", "revoke-all"} {
			s := newSessionWorld(ctx, t)
			s.store.failOn = op
			ok := in
			ok.Actor = s.u.id
			require.Error(t, s.change.Execute(ctx, ok), op)
			require.Zero(t, s.u.tv, op)
			require.Equal(t, 1, s.live(), op)
			require.Equal(t, user.PasswordHash("h:"+goodPassword), s.u.hash, op)
		}
	})
	t.Run("validation", func(t *testing.T) {
		s := newSessionWorld(ctx, t)
		for name, bad := range map[string]auth.ChangePasswordInput{
			"too short":  {Actor: s.u.id, CurrentPassword: goodPassword, NewPassword: "short"},
			"too long":   {Actor: s.u.id, CurrentPassword: goodPassword, NewPassword: string(make([]byte, 129))},
			"same":       {Actor: s.u.id, CurrentPassword: goodPassword, NewPassword: goodPassword},
			"no current": {Actor: s.u.id, CurrentPassword: "", NewPassword: newPassword},
			"no actor":   {CurrentPassword: goodPassword, NewPassword: newPassword},
		} {
			require.Error(t, s.change.Execute(ctx, bad), name)
		}
		requireCode(t, s.change.Execute(ctx, auth.ChangePasswordInput{Actor: s.u.id, CurrentPassword: goodPassword, NewPassword: "short"}), apperr.InvalidPassword)
		require.Zero(t, s.u.tv)
	})
}

func TestAuthenticate(t *testing.T) {
	ctx := t.Context()
	s := newSessionWorld(ctx, t)
	out, err := s.authn.Execute(ctx, auth.AuthenticateInput{AccessToken: s.first.AccessToken})
	require.NoError(t, err)
	require.Equal(t, s.u.id, out.Actor)

	_, err = s.authn.Execute(ctx, auth.AuthenticateInput{})
	requireCode(t, err, apperr.TokenInvalid)
	_, err = s.authn.Execute(ctx, auth.AuthenticateInput{AccessToken: "junk"})
	requireCode(t, err, apperr.TokenInvalid)

	s.u.tv++
	_, err = s.authn.Execute(ctx, auth.AuthenticateInput{AccessToken: s.first.AccessToken})
	requireCode(t, err, apperr.TokenInvalid)
	s.u.tv--

	s.u.status = user.StatusInactive
	_, err = s.authn.Execute(ctx, auth.AuthenticateInput{AccessToken: s.first.AccessToken})
	requireCode(t, err, apperr.TokenInvalid)
	s.u.status = user.StatusActive

	delete(s.store.users, s.u.id)
	_, err = s.authn.Execute(ctx, auth.AuthenticateInput{AccessToken: s.first.AccessToken})
	requireCode(t, err, apperr.TokenInvalid)
}
