// Package config is the only place that reads the process environment.
package config

import (
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
	DB        Database
	Auth      Auth
}

// Auth holds the authentication settings. The key directory is optional here
// (like the database URL); the authn adapter validates it when it is built.
type Auth struct {
	// JWTKeysDir holds <kid>.pem Ed25519 keys; keys are never read from the repository.
	JWTKeysDir      string        `env:"TB_JWT_KEYS_DIR"`
	JWTActiveKID    string        `env:"TB_JWT_ACTIVE_KID" envDefault:"k2026a"`
	JWTIssuer       string        `env:"TB_JWT_ISSUER" envDefault:"tenant-backoffice"`
	JWTAudience     string        `env:"TB_JWT_AUDIENCE" envDefault:"tenant-backoffice-api"`
	JWTLeeway       time.Duration `env:"TB_JWT_LEEWAY" envDefault:"30s"`
	AccessTTL       time.Duration `env:"TB_ACCESS_TTL" envDefault:"10m"`
	RefreshTTL      time.Duration `env:"TB_REFRESH_TTL" envDefault:"168h"`
	Argon2MemoryKiB uint32        `env:"TB_ARGON2_MEMORY_KIB" envDefault:"19456"`
	Argon2Time      uint32        `env:"TB_ARGON2_TIME" envDefault:"2"`
	Argon2Threads   uint8         `env:"TB_ARGON2_THREADS" envDefault:"1"`
	Argon2KeyLen    uint32        `env:"TB_ARGON2_KEY_LEN" envDefault:"32"`
	Argon2SaltLen   uint32        `env:"TB_ARGON2_SALT_LEN" envDefault:"16"`
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
	return cfg, nil
}

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
