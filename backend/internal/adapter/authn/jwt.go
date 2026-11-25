package authn

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// allowedMethods is the white list of signing algorithms. Nothing else is ever
// accepted: not "none", and not HS256 (which would let a public key act as a
// shared secret).
var allowedMethods = []string{"EdDSA"}

// claims are the access token claims: the registered ones plus the token
// version. Roles are deliberately absent: they are read from the database.
type claims struct {
	jwt.RegisteredClaims
	TokenVersion *int64 `json:"tv"`
}

// JWT signs and verifies access tokens. It implements port.TokenIssuer and
// port.TokenVerifier.
type JWT struct {
	cfg   Config
	keys  *keyring
	clock port.Clock
	// parser is built once; it is safe for concurrent use.
	parser *jwt.Parser
}

var (
	_ port.TokenIssuer   = (*JWT)(nil)
	_ port.TokenVerifier = (*JWT)(nil)
)

// NewJWT loads the keys from cfg.KeysDir. It fails when the active key is
// missing or has no private part.
func NewJWT(cfg Config, clock port.Clock) (*JWT, error) {
	if err := cfg.validateJWT(); err != nil {
		return nil, err
	}
	if clock == nil {
		return nil, errors.New("authn: clock is required")
	}
	keys, err := loadKeyring(cfg.KeysDir, cfg.ActiveKID)
	if err != nil {
		return nil, err
	}
	return &JWT{
		cfg: cfg, keys: keys, clock: clock,
		parser: jwt.NewParser(
			jwt.WithValidMethods(allowedMethods),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Audience),
			jwt.WithLeeway(cfg.Leeway),
			jwt.WithTimeFunc(clock.Now),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
		),
	}, nil
}

// Issue implements port.TokenIssuer.
func (j *JWT) Issue(sub user.ID, tokenVersion int64) (port.AccessToken, error) {
	now := j.clock.Now()
	exp := now.Add(j.cfg.AccessTTL)
	id, err := uuid.NewRandom()
	if err != nil {
		return port.AccessToken{}, fmt.Errorf("authn: generate jti: %w", err)
	}
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub.String(),
			Issuer:    j.cfg.Issuer,
			Audience:  jwt.ClaimStrings{j.cfg.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        id.String(),
		},
		TokenVersion: &tokenVersion,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	tok.Header["kid"] = j.keys.activeKID
	signed, err := tok.SignedString(j.keys.signing)
	if err != nil {
		return port.AccessToken{}, fmt.Errorf("authn: sign access token: %w", err)
	}
	return port.AccessToken{Token: signed, ExpiresAt: exp}, nil
}

// Verify implements port.TokenVerifier. Every failure is reported as
// apperr.TokenInvalid, except an expired token (apperr.TokenExpired). The token
// text never appears in an error.
func (j *JWT) Verify(token string) (port.AccessClaims, error) {
	var c claims
	if _, err := j.parser.ParseWithClaims(token, &c, j.keyFunc); err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return port.AccessClaims{}, apperr.TokenExpiredError()
		}
		return port.AccessClaims{}, apperr.TokenInvalidError()
	}
	sub, err := user.ParseID(c.Subject)
	if err != nil || sub.IsZero() || c.TokenVersion == nil {
		return port.AccessClaims{}, apperr.TokenInvalidError()
	}
	return port.AccessClaims{Subject: sub, TokenVersion: *c.TokenVersion}, nil
}

// keyFunc picks the verification key by the kid header. An unknown or absent
// kid is an error, never a fallback to the active key.
func (j *JWT) keyFunc(t *jwt.Token) (any, error) {
	if t.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
		return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
	}
	kid, ok := t.Header["kid"].(string)
	if !ok {
		return nil, errors.New("missing kid")
	}
	key, ok := j.keys.public[kid]
	if !ok {
		return nil, errors.New("unknown kid")
	}
	return ed25519.PublicKey(key), nil
}
