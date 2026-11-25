package app

import (
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/authn"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

// AuthnConfig maps the authentication settings of the process config to the
// authn adapter config.
func AuthnConfig(cfg config.Config) authn.Config {
	a := cfg.Auth
	return authn.Config{
		KeysDir:         a.JWTKeysDir,
		ActiveKID:       a.JWTActiveKID,
		Issuer:          a.JWTIssuer,
		Audience:        a.JWTAudience,
		AccessTTL:       a.AccessTTL,
		Leeway:          a.JWTLeeway,
		Argon2MemoryKiB: a.Argon2MemoryKiB,
		Argon2Time:      a.Argon2Time,
		Argon2Threads:   a.Argon2Threads,
		Argon2KeyLen:    a.Argon2KeyLen,
		Argon2SaltLen:   a.Argon2SaltLen,
	}
}

// AuthConfig maps the process config to the settings of the auth use cases.
func AuthConfig(cfg config.Config) auth.Config {
	return auth.Config{RefreshTTL: cfg.Auth.RefreshTTL}
}
