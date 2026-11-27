//go:build integration

package apitest_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

const (
	loginPath   = api + "/auth/login"
	refreshPath = api + "/auth/refresh"
	logoutPath  = api + "/auth/logout"
	passwdPath  = api + "/auth/password"
)

// session is a signed-in user: the client with the token and the refresh cookie.
type session struct {
	*apitest.Client
	refresh string
	tokens  gen.TokenResponse
}

func signIn(t *testing.T, s *apitest.Stack, email, password string) session {
	t.Helper()
	res := s.Login(email, password)
	requireStatus(t, res, http.StatusOK)
	tr := decode[gen.TokenResponse](t, res)
	c := res.Cookie("tb_refresh")
	require.NotNil(t, c)
	return session{Client: s.Anon.WithToken(tr.AccessToken), refresh: c.Value, tokens: tr}
}

// Proposed ID (not in spec 08 yet): T-A-31
func TestAPILoginAndCookieAttributes(t *testing.T) {
	s := apitest.NewStack(t)
	res := s.Login(s.World.Emails[s.World.AdminA], apitest.Password)
	requireStatus(t, res, http.StatusOK)

	tr := decode[gen.TokenResponse](t, res)
	require.NotEmpty(t, tr.AccessToken)
	require.Positive(t, tr.ExpiresIn)
	require.Equal(t, s.World.AdminA, tr.User.Id)

	c := res.Cookie("tb_refresh")
	require.NotNil(t, c, "login must set the refresh cookie")
	require.True(t, c.HttpOnly, "the refresh cookie must be HttpOnly")
	require.True(t, c.Secure, "the refresh cookie is Secure by default")
	require.Equal(t, http.SameSiteStrictMode, c.SameSite)
	require.Equal(t, "/api/v1/auth", c.Path)
	require.Positive(t, c.MaxAge)

	// The refresh token is only in the cookie: not in the body, not in the log.
	require.NotContains(t, string(res.Body), c.Value)
	require.NotContains(t, strings.ToLower(string(res.Body)), "refresh")
	require.NotContains(t, s.Log.String(), c.Value)
	require.NotContains(t, s.Log.String(), tr.AccessToken)
	require.NotContains(t, s.Log.String(), apitest.Password)
}

// Proposed ID (not in spec 08 yet): T-A-31
func TestAPICookieSecureIsConfigurable(t *testing.T) {
	s := apitest.NewStack(t, apitest.WithCookieSecure(false))
	res := s.Login(s.World.Emails[s.World.AdminA], apitest.Password)
	requireStatus(t, res, http.StatusOK)
	c := res.Cookie("tb_refresh")
	require.NotNil(t, c)
	require.False(t, c.Secure)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, c.SameSite)
}

// Proposed ID (not in spec 08 yet): T-A-32
// EC-107: a wrong password, an unknown email and an inactive user get the same answer.
func TestAPILoginFailuresAreIdentical(t *testing.T) {
	s := apitest.NewStack(t)
	cases := map[string][2]string{
		"wrong password": {s.World.Emails[s.World.AdminA], "wrong-password-123"},
		"unknown email":  {"nobody@example.test", apitest.Password},
		"inactive user":  {s.World.Emails[s.World.Inactive], apitest.Password},
		"empty password": {s.World.Emails[s.World.AdminA], ""},
	}
	var ref string
	for name, c := range cases {
		res := s.Login(c[0], c[1])
		res.RequireProblem(http.StatusUnauthorized, "invalid_credentials")
		require.Nil(t, res.Cookie("tb_refresh"), name)
		if ref == "" {
			ref = res.BodyWithoutRequestID()
		}
		require.Equal(t, ref, res.BodyWithoutRequestID(), name)
	}
}

// Test-ID: T-A-17
// EC-108: the sixth attempt in a minute for the same (address, email) is 429.
func TestAPILoginRateLimit(t *testing.T) {
	s := apitest.NewStack(t, apitest.WithDefaultRates())
	email := s.World.Emails[s.World.AdminA]
	for i := range 5 {
		res := s.Login(email, "wrong-password-123")
		require.Equal(t, http.StatusUnauthorized, res.Status, "attempt %d", i+1)
	}
	res := s.Login(email, "wrong-password-123")
	res.RequireProblem(http.StatusTooManyRequests, "rate_limited")
	require.NotEmpty(t, res.Header.Get("Retry-After"))
	// The right password does not get through a closed bucket either.
	s.Login(email, apitest.Password).RequireProblem(http.StatusTooManyRequests, "rate_limited")
	// Another email from the same address has its own pair bucket.
	require.Equal(t, http.StatusUnauthorized, s.Login("someone-else@example.test", "x").Status)
}

// Proposed ID (not in spec 08 yet): T-A-33
func TestAPIRefreshRequiresCSRFHeader(t *testing.T) {
	s := apitest.NewStack(t)
	u := signIn(t, s, s.World.Emails[s.World.AdminA], apitest.Password)

	// No header, a wrong value, no cookie: all refused, with the same answer.
	var ref string
	for name, opts := range map[string][]apitest.Option{
		"no header":    {apitest.WithCookie("tb_refresh", u.refresh)},
		"wrong value":  {apitest.WithCookie("tb_refresh", u.refresh), apitest.WithHeader("X-TB-CSRF", "0")},
		"no cookie":    {apitest.WithCSRF()},
		"garbage":      {apitest.WithCookie("tb_refresh", "x"), apitest.WithCSRF()},
		"empty cookie": {apitest.WithCookie("tb_refresh", ""), apitest.WithCSRF()},
	} {
		res := s.Anon.Post(refreshPath, nil, opts...)
		res.RequireProblem(http.StatusUnauthorized, "refresh_invalid")
		require.Nil(t, res.Cookie("tb_refresh"), name)
		if ref == "" {
			ref = res.BodyWithoutRequestID()
		}
		require.Equal(t, ref, res.BodyWithoutRequestID(), name)
	}

	// The refused attempts did not spend the token.
	res := s.Anon.Post(refreshPath, nil, apitest.WithCookie("tb_refresh", u.refresh), apitest.WithCSRF())
	requireStatus(t, res, http.StatusOK)
	next := res.Cookie("tb_refresh")
	require.NotNil(t, next)
	require.NotEqual(t, u.refresh, next.Value, "the refresh token rotates")
	require.True(t, next.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, next.SameSite)
	require.Equal(t, "/api/v1/auth", next.Path)
	tr := decode[gen.TokenResponse](t, res)
	require.Equal(t, s.World.AdminA, tr.User.Id)
	require.Equal(t, s.World.Emails[s.World.AdminA], string(tr.User.Email))
	require.NotContains(t, string(res.Body), next.Value)
	// The new access token works.
	requireStatus(t, s.Anon.WithToken(tr.AccessToken).Get(api+"/me"), http.StatusOK)
}

// Proposed ID (not in spec 08 yet): T-A-34
// EC-106: a used refresh token presented again revokes the whole family.
func TestAPIRefreshReplayRevokesFamily(t *testing.T) {
	s := apitest.NewStack(t)
	u := signIn(t, s, s.World.Emails[s.World.AdminA], apitest.Password)
	refresh := func(token string) *apitest.Response {
		return s.Anon.Post(refreshPath, nil, apitest.WithCookie("tb_refresh", token), apitest.WithCSRF())
	}

	first := refresh(u.refresh)
	requireStatus(t, first, http.StatusOK)
	second := first.Cookie("tb_refresh")
	require.NotNil(t, second)

	// Replay of the first (used) token: refused, and the family dies.
	refresh(u.refresh).RequireProblem(http.StatusUnauthorized, "refresh_invalid")
	refresh(second.Value).RequireProblem(http.StatusUnauthorized, "refresh_invalid")
	require.NotContains(t, s.Log.String(), u.refresh)
	require.NotContains(t, s.Log.String(), second.Value)

	// Another session of the same user is a different family and stays valid.
	other := signIn(t, s, s.World.Emails[s.World.AdminA], apitest.Password)
	requireStatus(t, refresh(other.refresh), http.StatusOK)
}

// Proposed ID (not in spec 08 yet): T-A-35
func TestAPILogout(t *testing.T) {
	s := apitest.NewStack(t)
	u := signIn(t, s, s.World.Emails[s.World.AdminA], apitest.Password)

	// Needs the access token ...
	s.Anon.Post(logoutPath, nil, apitest.WithCookie("tb_refresh", u.refresh), apitest.WithCSRF()).
		RequireProblem(http.StatusUnauthorized, "token_missing")
	// ... and the CSRF header; without it nothing happens.
	u.Post(logoutPath, nil, apitest.WithCookie("tb_refresh", u.refresh)).
		RequireProblem(http.StatusUnauthorized, "token_invalid")
	res := s.Anon.Post(refreshPath, nil, apitest.WithCookie("tb_refresh", u.refresh), apitest.WithCSRF())
	requireStatus(t, res, http.StatusOK)
	live := res.Cookie("tb_refresh")

	out := u.Post(logoutPath, nil, apitest.WithCookie("tb_refresh", live.Value), apitest.WithCSRF())
	requireStatus(t, out, http.StatusNoContent)
	cleared := out.Cookie("tb_refresh")
	require.NotNil(t, cleared)
	require.Empty(t, cleared.Value)
	require.Negative(t, cleared.MaxAge)
	require.True(t, cleared.HttpOnly)

	s.Anon.Post(refreshPath, nil, apitest.WithCookie("tb_refresh", live.Value), apitest.WithCSRF()).
		RequireProblem(http.StatusUnauthorized, "refresh_invalid")

	// A missing or unknown cookie is still a success.
	requireStatus(t, u.Post(logoutPath, nil, apitest.WithCSRF()), http.StatusNoContent)
	requireStatus(t, u.Post(logoutPath, nil, apitest.WithCookie("tb_refresh", "unknown"), apitest.WithCSRF()), http.StatusNoContent)
}

// Test-ID: T-A-16
// EC-105: a password change invalidates the old access token and every refresh family.
func TestAPIStaleTokenVersion(t *testing.T) {
	s := apitest.NewStack(t)
	email := s.World.Emails[s.World.AdminA]
	u := signIn(t, s, email, apitest.Password)
	requireStatus(t, u.Get(api+"/me"), http.StatusOK)

	const newPassword = "another-long-password-2"
	// Wrong current password and too short a new one are refused first.
	u.Post(passwdPath, gen.ChangePasswordRequest{CurrentPassword: "wrong-password-123", NewPassword: newPassword}).
		RequireProblem(http.StatusUnauthorized, "invalid_credentials")
	u.Post(passwdPath, gen.ChangePasswordRequest{CurrentPassword: apitest.Password, NewPassword: "short"}).
		RequireProblem(http.StatusBadRequest, "invalid_password")
	requireStatus(t, u.Get(api+"/me"), http.StatusOK)

	requireStatus(t, u.Post(passwdPath, gen.ChangePasswordRequest{CurrentPassword: apitest.Password, NewPassword: newPassword}), http.StatusNoContent)

	u.Get(api+"/me").RequireProblem(http.StatusUnauthorized, "token_invalid")
	s.Anon.Post(refreshPath, nil, apitest.WithCookie("tb_refresh", u.refresh), apitest.WithCSRF()).
		RequireProblem(http.StatusUnauthorized, "refresh_invalid")
	s.Login(email, apitest.Password).RequireProblem(http.StatusUnauthorized, "invalid_credentials")
	fresh := signIn(t, s, email, newPassword)
	requireStatus(t, fresh.Get(api+"/me"), http.StatusOK)
	require.NotContains(t, s.Log.String(), newPassword)
}
