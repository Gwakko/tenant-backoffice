package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
)

const (
	firstConnectDelay = 100 * time.Millisecond
	maxConnectDelay   = time.Second
)

// ConnectDB opens the pool like postgres.NewPool but keeps retrying with a
// growing delay until cfg.ConnectTimeout has passed, so that a process that
// starts before the database (EC-135) waits for it instead of dying at once.
// A malformed URL is not retried.
func ConnectDB(ctx context.Context, cfg postgres.Config) (*pgxpool.Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if _, err := pgxpool.ParseConfig(cfg.URL); err != nil {
		return nil, errors.New("postgres: invalid database URL")
	}
	timeout := cfg.ConnectTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	delay := firstConnectDelay
	for {
		pool, err := postgres.NewPool(ctx, cfg)
		if err == nil {
			return pool, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || ctx.Err() != nil {
			return nil, fmt.Errorf("database not ready after %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database not ready: %w", ctx.Err())
		case <-time.After(min(delay, remaining)):
		}
		delay = min(delay*2, maxConnectDelay)
	}
}

// DBReadyCheck is the readiness check of /readyz: the database must answer.
func DBReadyCheck(pool *pgxpool.Pool) httpapi.ReadyCheck {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return pool.Ping(ctx)
	}
}
