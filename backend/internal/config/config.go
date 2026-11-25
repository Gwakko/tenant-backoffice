// Package config is the only place that reads the process environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Config holds the settings of the whole process. Adapter configs are
// assembled from it by the composition root.
type Config struct {
	HTTPAddr  string `env:"TB_HTTP_ADDR,required,notEmpty"`
	LogLevel  string `env:"TB_LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"TB_LOG_FORMAT" envDefault:"json"`
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
