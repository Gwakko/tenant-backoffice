package user

import (
	"unicode/utf8"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// Password length bounds, in bytes of the UTF-8 encoding.
const (
	MinPasswordBytes = 12
	MaxPasswordBytes = 128
)

// PasswordHash is an opaque PHC-format hash string produced by a Hasher.
type PasswordHash string

// ValidatePassword checks the plain password rules: valid UTF-8, 12..128 bytes.
func ValidatePassword(plain string) error {
	if !utf8.ValidString(plain) {
		return apperr.New(apperr.InvalidPassword, "password must be valid UTF-8")
	}
	if n := len(plain); n < MinPasswordBytes || n > MaxPasswordBytes {
		return apperr.New(apperr.InvalidPassword, "password length must be 12..128 bytes")
	}
	return nil
}

// Hasher hashes and verifies passwords. The argon2id implementation lives in
// the authn adapter; the domain only knows this port. Hash must be called with
// a password that passed ValidatePassword.
type Hasher interface {
	Hash(plain string) (PasswordHash, error)
	// Verify reports whether plain matches hash. A malformed hash is an error.
	Verify(hash PasswordHash, plain string) (bool, error)
}
