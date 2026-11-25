package authn_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

type rawClaims struct {
	jwt.RegisteredClaims
	TV int64 `json:"tv"`
}

// fixture is a JWT adapter over a fresh key directory with one key k1.
type fixture struct {
	dir   string
	clock *fakeClock
	priv  ed25519.PrivateKey
	jwt   *authn.JWT
	sub   user.ID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{dir: dir, clock: newClock(), sub: user.ID(uuid.New())}
	f.priv = writeKey(t, dir, "k1", false)
	j, err := authn.NewJWT(testConfig(dir, "k1"), f.clock)
	require.NoError(t, err)
	f.jwt = j
	return f
}

// claimsAt builds valid claims issued at the fixture clock.
func (f *fixture) claimsAt(now time.Time) rawClaims {
	return rawClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   f.sub.String(),
			Issuer:    "tenant-backoffice",
			Audience:  jwt.ClaimStrings{"tenant-backoffice-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
			ID:        uuid.NewString(),
		},
		TV: 3,
	}
}

func signWith(t *testing.T, method jwt.SigningMethod, kid string, key any, c rawClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, c)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	require.NoError(t, err)
	return s
}

func requireCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	require.Error(t, err)
	code, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, want, code)
}

func TestIssueAndVerify(t *testing.T) {
	f := newFixture(t)
	at, err := f.jwt.Issue(f.sub, 7)
	require.NoError(t, err)
	require.Equal(t, f.clock.Now().Add(10*time.Minute), at.ExpiresAt)

	got, err := f.jwt.Verify(at.Token)
	require.NoError(t, err)
	require.Equal(t, f.sub, got.Subject)
	require.EqualValues(t, 7, got.TokenVersion)

	hdr := decodePart(t, at.Token, 0)
	require.Equal(t, "EdDSA", hdr["alg"])
	require.Equal(t, "k1", hdr["kid"])
	pl := decodePart(t, at.Token, 1)
	require.Equal(t, "tenant-backoffice", pl["iss"])
	require.Equal(t, []any{"tenant-backoffice-api"}, pl["aud"])
	for _, k := range []string{"sub", "tv", "iat", "nbf", "exp", "jti"} {
		require.Contains(t, pl, k)
	}
	require.NotContains(t, pl, "roles")
}

// Test-ID: T-U-30
func TestExpired(t *testing.T) {
	f := newFixture(t)
	at, err := f.jwt.Issue(f.sub, 1)
	require.NoError(t, err)

	f.clock.Advance(10*time.Minute + 29*time.Second) // inside the leeway
	_, err = f.jwt.Verify(at.Token)
	require.NoError(t, err)

	f.clock.Advance(2 * time.Second) // past exp + leeway
	_, err = f.jwt.Verify(at.Token)
	requireCode(t, err, apperr.TokenExpired)

	f.clock.Advance(24 * time.Hour)
	_, err = f.jwt.Verify(at.Token)
	requireCode(t, err, apperr.TokenExpired)
}

// Test-ID: T-U-31
func TestForged(t *testing.T) {
	f := newFixture(t)
	good := f.claimsAt(f.clock.Now())
	pub := f.priv.Public().(ed25519.PublicKey)

	t.Run("alg none", func(t *testing.T) {
		tok := signWith(t, jwt.SigningMethodNone, "k1", jwt.UnsafeAllowNoneSignatureType, good)
		_, err := f.jwt.Verify(tok)
		requireCode(t, err, apperr.TokenInvalid)
		// The same with the signature part stripped, as an attacker would send.
		_, err = f.jwt.Verify(strings.TrimSuffix(tok, ".") + ".")
		requireCode(t, err, apperr.TokenInvalid)
	})
	t.Run("HS256 with the public key as the secret", func(t *testing.T) {
		for _, m := range []jwt.SigningMethod{jwt.SigningMethodHS256, jwt.SigningMethodHS384, jwt.SigningMethodHS512} {
			tok := signWith(t, m, "k1", []byte(pub), good)
			_, err := f.jwt.Verify(tok)
			requireCode(t, err, apperr.TokenInvalid)
		}
	})
	t.Run("changed payload keeps the old signature", func(t *testing.T) {
		valid := signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, good)
		parts := strings.Split(valid, ".")
		evil := good
		evil.TV = 99
		raw, err := json.Marshal(evil)
		require.NoError(t, err)
		parts[1] = base64.RawURLEncoding.EncodeToString(raw)
		_, err = f.jwt.Verify(strings.Join(parts, "."))
		requireCode(t, err, apperr.TokenInvalid)
	})
	t.Run("signed by another key under a known kid", func(t *testing.T) {
		other := writeKey(t, t.TempDir(), "k1", false)
		tok := signWith(t, jwt.SigningMethodEdDSA, "k1", other, good)
		_, err := f.jwt.Verify(tok)
		requireCode(t, err, apperr.TokenInvalid)
	})
	t.Run("issuer and audience", func(t *testing.T) {
		badIss := good
		badIss.Issuer = "someone-else"
		_, err := f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, badIss))
		requireCode(t, err, apperr.TokenInvalid)

		badAud := good
		badAud.Audience = jwt.ClaimStrings{"another-api"}
		_, err = f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, badAud))
		requireCode(t, err, apperr.TokenInvalid)

		noAud := good
		noAud.Audience = nil
		_, err = f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, noAud))
		requireCode(t, err, apperr.TokenInvalid)
	})
	t.Run("required claims", func(t *testing.T) {
		noExp := good
		noExp.ExpiresAt = nil
		_, err := f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, noExp))
		requireCode(t, err, apperr.TokenInvalid)

		badSub := good
		badSub.Subject = "not-a-uuid"
		_, err = f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, badSub))
		requireCode(t, err, apperr.TokenInvalid)

		// A token without tv must not verify as version 0.
		noTV := jwt.NewWithClaims(jwt.SigningMethodEdDSA, good.RegisteredClaims)
		noTV.Header["kid"] = "k1"
		s, err := noTV.SignedString(f.priv)
		require.NoError(t, err)
		_, err = f.jwt.Verify(s)
		requireCode(t, err, apperr.TokenInvalid)
	})
	t.Run("garbage", func(t *testing.T) {
		for _, s := range []string{"", "abc", "a.b.c", "....", strings.Repeat("A", 5000)} {
			_, err := f.jwt.Verify(s)
			requireCode(t, err, apperr.TokenInvalid)
		}
	})
	t.Run("the control token is accepted", func(t *testing.T) {
		_, err := f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, good))
		require.NoError(t, err)
	})
}

// Test-ID: T-U-32
func TestKeyRotation(t *testing.T) {
	dir := t.TempDir()
	clock := newClock()
	writeKey(t, dir, "k1", false)
	oldJWT, err := authn.NewJWT(testConfig(dir, "k1"), clock)
	require.NoError(t, err)
	sub := user.ID(uuid.New())
	oldToken, err := oldJWT.Issue(sub, 1)
	require.NoError(t, err)

	// Step 1: the new key is added and made active; both are in the directory.
	writeKey(t, dir, "k2", false)
	both, err := authn.NewJWT(testConfig(dir, "k2"), clock)
	require.NoError(t, err)
	_, err = both.Verify(oldToken.Token)
	require.NoError(t, err, "a token of the old key stays valid while both keys are present")
	newToken, err := both.Issue(sub, 1)
	require.NoError(t, err)
	require.Equal(t, "k2", decodePart(t, newToken.Token, 0)["kid"])
	_, err = both.Verify(newToken.Token)
	require.NoError(t, err)

	// Step 2: the old key is removed after the access TTL.
	require.NoError(t, removeKey(dir, "k1"))
	only, err := authn.NewJWT(testConfig(dir, "k2"), clock)
	require.NoError(t, err)
	_, err = only.Verify(oldToken.Token)
	requireCode(t, err, apperr.TokenInvalid)
	_, err = only.Verify(newToken.Token)
	require.NoError(t, err)

	// A kid that never existed.
	f := newFixture(t)
	tok := signWith(t, jwt.SigningMethodEdDSA, "ghost", f.priv, f.claimsAt(f.clock.Now()))
	_, err = f.jwt.Verify(tok)
	requireCode(t, err, apperr.TokenInvalid)
	// No kid at all does not fall back to the active key.
	tok = signWith(t, jwt.SigningMethodEdDSA, "", f.priv, f.claimsAt(f.clock.Now()))
	_, err = f.jwt.Verify(tok)
	requireCode(t, err, apperr.TokenInvalid)
}

func TestKeyLoading(t *testing.T) {
	t.Run("a public-only old key verifies but never signs", func(t *testing.T) {
		dir := t.TempDir()
		clock := newClock()
		oldPriv := writeKey(t, dir, "old", false)
		// Replace the old private key by its public part.
		writeKey(t, dir, "k2", false)
		require.NoError(t, removeKey(dir, "old"))
		writePublicOf(t, dir, "old", oldPriv)
		j, err := authn.NewJWT(testConfig(dir, "k2"), clock)
		require.NoError(t, err)
		c := rawClaims{RegisteredClaims: jwt.RegisteredClaims{
			Subject: uuid.NewString(), Issuer: "tenant-backoffice", Audience: jwt.ClaimStrings{"tenant-backoffice-api"},
			IssuedAt: jwt.NewNumericDate(clock.Now()), ExpiresAt: jwt.NewNumericDate(clock.Now().Add(time.Minute)),
		}}
		_, err = j.Verify(signWith(t, jwt.SigningMethodEdDSA, "old", oldPriv, c))
		require.NoError(t, err)
	})
	t.Run("the active key must exist and hold a private part", func(t *testing.T) {
		dir := t.TempDir()
		writeKey(t, dir, "k1", true)
		_, err := authn.NewJWT(testConfig(dir, "k1"), newClock())
		require.Error(t, err)
		_, err = authn.NewJWT(testConfig(dir, "missing"), newClock())
		require.Error(t, err)
	})
	t.Run("a bad configuration is rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeKey(t, dir, "k1", false)
		cfg := testConfig(dir, "k1")
		cfg.AccessTTL = 0
		_, err := authn.NewJWT(cfg, newClock())
		require.Error(t, err)
		_, err = authn.NewJWT(testConfig("", "k1"), newClock())
		require.Error(t, err)
		_, err = authn.NewJWT(testConfig(t.TempDir()+"/nope", "k1"), newClock())
		require.Error(t, err)
	})
}

// Test-ID: T-U-36
func TestClockSkew(t *testing.T) {
	f := newFixture(t)
	now := f.clock.Now()

	for _, ahead := range []time.Duration{20 * time.Second} {
		c := f.claimsAt(now.Add(ahead)) // iat and nbf in the future
		c.ExpiresAt = jwt.NewNumericDate(now.Add(10 * time.Minute))
		_, err := f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, c))
		require.NoError(t, err, "%s ahead is within the 30 s leeway", ahead)
	}
	for _, ahead := range []time.Duration{60 * time.Second} {
		c := f.claimsAt(now.Add(ahead))
		c.ExpiresAt = jwt.NewNumericDate(now.Add(10 * time.Minute))
		_, err := f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, c))
		requireCode(t, err, apperr.TokenInvalid)
	}
	// nbf alone and iat alone.
	c := f.claimsAt(now)
	c.NotBefore = jwt.NewNumericDate(now.Add(60 * time.Second))
	_, err := f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, c))
	requireCode(t, err, apperr.TokenInvalid)
	c = f.claimsAt(now)
	c.IssuedAt = jwt.NewNumericDate(now.Add(60 * time.Second))
	_, err = f.jwt.Verify(signWith(t, jwt.SigningMethodEdDSA, "k1", f.priv, c))
	requireCode(t, err, apperr.TokenInvalid)
}

func decodePart(t *testing.T, token string, i int) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[i])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}
