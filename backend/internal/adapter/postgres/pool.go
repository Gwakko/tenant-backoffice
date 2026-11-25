// Package postgres implements the storage ports on PostgreSQL through pgx. The
// service connects as the role tb_app, which row-level security always applies
// to; the acting user is carried per transaction (see TxRunner).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config configures the connection pool.
type Config struct {
	// URL is the connection string of the application database for tb_app.
	URL string
	// MaxConns is the pool size.
	MaxConns int32
	// StatementTimeout is the server-side limit of one statement.
	StatementTimeout time.Duration
	// IdleInTxTimeout makes the server end a session that sits idle inside a
	// transaction, so a lost client cannot hold locks forever.
	IdleInTxTimeout time.Duration
	// ConnectTimeout limits establishing one connection.
	ConnectTimeout time.Duration
}

// Validate checks the settings that have no safe default.
func (c Config) Validate() error {
	switch {
	case c.URL == "":
		return errors.New("postgres: database URL is required")
	case c.MaxConns < 1:
		return fmt.Errorf("postgres: MaxConns must be at least 1, got %d", c.MaxConns)
	case c.StatementTimeout <= 0:
		return errors.New("postgres: StatementTimeout must be positive")
	case c.IdleInTxTimeout <= 0:
		return errors.New("postgres: IdleInTxTimeout must be positive")
	}
	return nil
}

// NewPool opens the pool and checks that the database answers. The timeouts are
// sent as session parameters at connect time, so they hold for every
// connection the pool ever creates.
func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.New("postgres: invalid database URL")
	}
	pc.MaxConns = cfg.MaxConns
	if cfg.ConnectTimeout > 0 {
		pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	if pc.ConnConfig.RuntimeParams == nil {
		pc.ConnConfig.RuntimeParams = map[string]string{}
	}
	pc.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)
	pc.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = strconv.FormatInt(cfg.IdleInTxTimeout.Milliseconds(), 10)

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}
