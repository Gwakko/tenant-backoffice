// Package config is the only place that reads the process environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds the settings of the whole process. Adapter configs are
// assembled from it by the composition root.
type Config struct {
	HTTPAddr  string `env:"TB_HTTP_ADDR,required,notEmpty"`
	LogLevel  string `env:"TB_LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"TB_LOG_FORMAT" envDefault:"json"`
	// Currency is the ISO 4217 code of the installation. It is shown with
	// balances; amounts are stored without a currency (spec 01 section 3).
	Currency string `env:"TB_CURRENCY" envDefault:"EUR"`
	DB       Database
	Auth     Auth
	HTTP     HTTP
	Rate     RateLimit
}

// HTTP holds the transport settings. Defaults are the safe ones: the client
// address is the TCP peer, and an unset cursor key means a random key per
// process (cursors stop working after a restart, nothing is ever signed with
// a guessable key).
type HTTP struct {
	// MaxBodyBytes bounds a request body (spec 03 section 1: 64 KiB).
	MaxBodyBytes int64 `env:"TB_HTTP_MAX_BODY_BYTES" envDefault:"65536"`
	// TrustForwardedFor takes the client address from the last X-Forwarded-For
	// entry. Enable it only behind a proxy that appends the real peer address.
	TrustForwardedFor bool `env:"TB_HTTP_TRUST_FORWARDED_FOR" envDefault:"false"`
	// CursorKey signs pagination cursors: base64 (std or url), at least 32 bytes
	// after decoding. CursorKeyPrevious is the key being rotated out; cursors
	// signed with it still verify.
	CursorKey         string        `env:"TB_CURSOR_KEY"`
	CursorKeyPrevious string        `env:"TB_CURSOR_KEY_PREVIOUS"`
	CursorTTL         time.Duration `env:"TB_CURSOR_TTL" envDefault:"1h"`
}

// RateLimit holds the request budgets per minute (spec 02 section 7).
type RateLimit struct {
	LoginPairPerMin int `env:"TB_RATE_LOGIN_PAIR_PER_MIN" envDefault:"5"`
	LoginIPPerMin   int `env:"TB_RATE_LOGIN_IP_PER_MIN" envDefault:"20"`
	RefreshIPPerMin int `env:"TB_RATE_REFRESH_IP_PER_MIN" envDefault:"20"`
	MutationPerMin  int `env:"TB_RATE_MUTATION_PER_MIN" envDefault:"60"`
	ReadPerMin      int `env:"TB_RATE_READ_PER_MIN" envDefault:"600"`
	// MaxKeys bounds the in-memory limiter state.
	MaxKeys int `env:"TB_RATE_MAX_KEYS" envDefault:"100000"`
}

// minCursorKeyBytes is the shortest accepted HMAC key.
const minCursorKeyBytes = 32

// CursorKeys decodes the configured cursor keys, the current one first. It
// returns no keys when TB_CURSOR_KEY is unset.
func (h HTTP) CursorKeys() ([][]byte, error) {
	var out [][]byte
	for _, v := range []struct{ name, val string }{
		{"TB_CURSOR_KEY", h.CursorKey}, {"TB_CURSOR_KEY_PREVIOUS", h.CursorKeyPrevious},
	} {
		if v.val == "" {
			continue
		}
		raw, err := decodeKey(v.val)
		if err != nil {
			return nil, fmt.Errorf("load config: %s is not valid base64", v.name)
		}
		if len(raw) < minCursorKeyBytes {
			return nil, fmt.Errorf("load config: %s must decode to at least %d bytes", v.name, minCursorKeyBytes)
		}
		out = append(out, raw)
	}
	if h.CursorKey == "" && h.CursorKeyPrevious != "" {
		return nil, errors.New("load config: TB_CURSOR_KEY_PREVIOUS requires TB_CURSOR_KEY")
	}
	return out, nil
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(s); err == nil {
			return raw, nil
		}
	}
	return nil, errors.New("not base64")
}

func (c Config) validateHTTP() error {
	if c.HTTP.MaxBodyBytes < 1 || c.HTTP.MaxBodyBytes > 8<<20 {
		return fmt.Errorf("load config: TB_HTTP_MAX_BODY_BYTES must be in 1..%d, got %d", 8<<20, c.HTTP.MaxBodyBytes)
	}
	if c.HTTP.CursorTTL <= 0 {
		return fmt.Errorf("load config: TB_CURSOR_TTL must be positive, got %s", c.HTTP.CursorTTL)
	}
	if _, err := c.HTTP.CursorKeys(); err != nil {
		return err
	}
	for _, v := range []struct {
		name string
		val  int
	}{
		{"TB_RATE_LOGIN_PAIR_PER_MIN", c.Rate.LoginPairPerMin},
		{"TB_RATE_LOGIN_IP_PER_MIN", c.Rate.LoginIPPerMin},
		{"TB_RATE_REFRESH_IP_PER_MIN", c.Rate.RefreshIPPerMin},
		{"TB_RATE_MUTATION_PER_MIN", c.Rate.MutationPerMin},
		{"TB_RATE_READ_PER_MIN", c.Rate.ReadPerMin},
		{"TB_RATE_MAX_KEYS", c.Rate.MaxKeys},
	} {
		if v.val < 1 {
			return fmt.Errorf("load config: %s must be positive, got %d", v.name, v.val)
		}
	}
	return nil
}

// Auth holds the authentication settings. The key directory is optional here
// (like the database URL); the authn adapter validates it when it is built.
type Auth struct {
	// JWTKeysDir holds <kid>.pem Ed25519 keys; keys are never read from the repository.
	JWTKeysDir   string        `env:"TB_JWT_KEYS_DIR"`
	JWTActiveKID string        `env:"TB_JWT_ACTIVE_KID" envDefault:"k2026a"`
	JWTIssuer    string        `env:"TB_JWT_ISSUER" envDefault:"tenant-backoffice"`
	JWTAudience  string        `env:"TB_JWT_AUDIENCE" envDefault:"tenant-backoffice-api"`
	JWTLeeway    time.Duration `env:"TB_JWT_LEEWAY" envDefault:"30s"`
	AccessTTL    time.Duration `env:"TB_ACCESS_TTL" envDefault:"10m"`
	RefreshTTL   time.Duration `env:"TB_REFRESH_TTL" envDefault:"168h"`
	// CookieSecure marks the refresh cookie Secure (HTTPS only). Turn it off
	// only for local work over plain HTTP.
	CookieSecure    bool   `env:"TB_COOKIE_SECURE" envDefault:"true"`
	Argon2MemoryKiB uint32 `env:"TB_ARGON2_MEMORY_KIB" envDefault:"19456"`
	Argon2Time      uint32 `env:"TB_ARGON2_TIME" envDefault:"2"`
	Argon2Threads   uint8  `env:"TB_ARGON2_THREADS" envDefault:"1"`
	Argon2KeyLen    uint32 `env:"TB_ARGON2_KEY_LEN" envDefault:"32"`
	Argon2SaltLen   uint32 `env:"TB_ARGON2_SALT_LEN" envDefault:"16"`
}

// Database holds the runtime connection settings of the application database.
// URL is optional here (the process starts without a database until the
// services that need one are wired); the postgres adapter validates it.
type Database struct {
	// URL connects as the application role tb_app.
	URL              string        `env:"TB_DB_URL"`
	MaxConns         int32         `env:"TB_DB_MAX_CONNS" envDefault:"10"`
	StatementTimeout time.Duration `env:"TB_DB_STATEMENT_TIMEOUT" envDefault:"5s"`
	IdleInTxTimeout  time.Duration `env:"TB_DB_IDLE_IN_TX_TIMEOUT" envDefault:"10s"`
	ConnectTimeout   time.Duration `env:"TB_DB_CONNECT_TIMEOUT" envDefault:"30s"`
}

// Load reads and validates the environment. Errors name the offending variable.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	if _, err := cfg.SlogLevel(); err != nil {
		return Config{}, err
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return Config{}, fmt.Errorf("load config: TB_LOG_FORMAT must be json or text, got %q", cfg.LogFormat)
	}
	if err := cfg.validateHTTP(); err != nil {
		return Config{}, err
	}
	if !validCurrency(cfg.Currency) {
		return Config{}, fmt.Errorf("load config: TB_CURRENCY must be three upper-case letters, got %q", cfg.Currency)
	}
	return cfg, nil
}

func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// DefaultCurrency is used when the config is built without TB_CURRENCY (tests
// that fill the struct by hand).
const DefaultCurrency = "EUR"

// SlogLevel converts LogLevel to a slog level.
func (c Config) SlogLevel() (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToLower(c.LogLevel))); err != nil {
		return 0, fmt.Errorf("load config: TB_LOG_LEVEL invalid value %q: %w", c.LogLevel, err)
	}
	return l, nil
}

// NewLogger builds the process logger according to the config.
func (c Config) NewLogger() (*slog.Logger, error) {
	level, err := c.SlogLevel()
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}
	if c.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts)), nil
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts)), nil
}
