package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	refreshBytes = 32
	// RefreshTokenLen is the length of the base64url text of a refresh token.
	RefreshTokenLen = 43
)

var b64url = base64.RawURLEncoding

// RefreshCodec implements port.RefreshCodec: a token is 32 random bytes in
// base64url, the database keeps only its sha256.
type RefreshCodec struct{}

var _ port.RefreshCodec = RefreshCodec{}

// NewRefreshCodec returns the codec.
func NewRefreshCodec() RefreshCodec { return RefreshCodec{} }

// Generate implements port.RefreshCodec.
func (RefreshCodec) Generate() (string, []byte, error) {
	raw := make([]byte, refreshBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("authn: read random bytes: %w", err)
	}
	token := b64url.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

// Hash implements port.RefreshCodec.
func (RefreshCodec) Hash(token string) ([]byte, error) {
	if len(token) != RefreshTokenLen {
		return nil, apperr.RefreshInvalidError()
	}
	raw, err := b64url.Strict().DecodeString(token)
	if err != nil || len(raw) != refreshBytes {
		return nil, apperr.RefreshInvalidError()
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:], nil
}
