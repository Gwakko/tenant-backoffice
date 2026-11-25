// Package authn implements the authentication primitives: argon2id password
// hashing, EdDSA access tokens with key rotation and opaque refresh tokens.
package authn

import (
	"errors"
	"fmt"
	"time"
)

// Config configures the package. The argon2id parameters live here so that
// they can be raised without touching code; Verify reads the parameters out of
// each stored hash, so old hashes keep working after a change.
type Config struct {
	// KeysDir holds the Ed25519 keys as <kid>.pem files: a PKCS#8 private key,
	// or a public key (PKIX) that is only accepted for verification.
	KeysDir string
	// ActiveKID names the key that signs new tokens; it needs a private key.
	ActiveKID string
	Issuer    string
	Audience  string
	// AccessTTL is the lifetime of an access token.
	AccessTTL time.Duration
	// Leeway tolerates clock skew in exp, nbf and iat.
	Leeway time.Duration

	// Argon2id parameters (PHC string format).
	Argon2MemoryKiB uint32
	Argon2Time      uint32
	Argon2Threads   uint8
	Argon2KeyLen    uint32
	Argon2SaltLen   uint32
}

// Validate checks the settings that have no safe default.
func (c Config) Validate() error {
	switch {
	case c.Argon2MemoryKiB < 8 || c.Argon2Time < 1 || c.Argon2Threads < 1:
		return errors.New("authn: argon2 memory (>= 8 KiB), time and threads must be positive")
	case c.Argon2KeyLen < 16 || c.Argon2SaltLen < 8:
		return errors.New("authn: argon2 key length (>= 16) and salt length (>= 8) are too small")
	}
	return nil
}

func (c Config) validateJWT() error {
	switch {
	case c.KeysDir == "":
		return errors.New("authn: keys directory is required")
	case c.ActiveKID == "":
		return errors.New("authn: active key id is required")
	case c.Issuer == "" || c.Audience == "":
		return errors.New("authn: issuer and audience are required")
	case c.AccessTTL <= 0:
		return fmt.Errorf("authn: access TTL must be positive, got %s", c.AccessTTL)
	case c.Leeway < 0:
		return fmt.Errorf("authn: leeway must not be negative, got %s", c.Leeway)
	}
	return nil
}
