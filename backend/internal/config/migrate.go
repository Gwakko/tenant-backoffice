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
