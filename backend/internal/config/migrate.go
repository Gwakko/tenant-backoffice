package config

import (
	"fmt"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
)

// Migrate holds the settings of the migration tool. Which URL is required
// depends on the command, so presence is checked by the caller.
type Migrate struct {
	// MigrateURL is the tb_owner URL of the application database.
	MigrateURL string `env:"TB_DB_MIGRATE_URL"`
	// AdminURL is the superuser URL used by roundtrip to create a scratch database.
	AdminURL       string        `env:"TB_TEST_DB_ADMIN_URL"`
	ConnectTimeout time.Duration `env:"TB_DB_CONNECT_TIMEOUT" envDefault:"30s"`
}

// LoadMigrate reads the migration tool settings from the environment.
func LoadMigrate() (Migrate, error) {
	var cfg Migrate
	if err := env.Parse(&cfg); err != nil {
		return Migrate{}, fmt.Errorf("load migrate config: %w", err)
	}
	return cfg, nil
}

// TestDBAdminURL returns the superuser URL tests use to create databases.
// The boolean is false when the variable is unset or empty.
func TestDBAdminURL() (string, bool) {
	v, ok := os.LookupEnv("TB_TEST_DB_ADMIN_URL")
	return v, ok && v != ""
}

// LoadSeed reads the settings of the seed tool: the migrate settings and the
// password hashing parameters. Unlike Load it needs no HTTP settings, because
// the tool runs without them.
func LoadSeed() (Migrate, Auth, error) {
	mig, err := LoadMigrate()
	if err != nil {
		return Migrate{}, Auth{}, err
	}
	var a Auth
	if err := env.Parse(&a); err != nil {
		return Migrate{}, Auth{}, fmt.Errorf("load seed config: %w", err)
	}
	return mig, a, nil
}
