// Package app is the composition root: the only place where adapters and use
// cases are constructed and wired together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

const shutdownTimeout = 15 * time.Second

// API is the assembled HTTP service.
type API struct {
	server *http.Server
	log    *slog.Logger
	pool   *pgxpool.Pool
}

// Handler is the root handler of the service (for tests that serve it).
func (a *API) Handler() http.Handler { return a.server.Handler }

// Close releases the database pool. Run closes it itself after the shutdown.
func (a *API) Close() {
	if a.pool != nil {
		a.pool.Close()
	}
}

// BuildAPI wires the API process from the config.
func BuildAPI(cfg config.Config) (*API, error) {
	logger, err := cfg.NewLogger()
	if err != nil {
		return nil, err
	}
	return BuildAPIWith(cfg, logger)
}

// BuildAPIWith is BuildAPI with the logger supplied by the caller (tests
// capture it to check that no secret is logged).
func BuildAPIWith(cfg config.Config, logger *slog.Logger) (*API, error) {
	var err error
	clock := SystemClock{}
	cursors, err := CursorCodec(cfg, clock, logger)
	if err != nil {
		return nil, err
	}
	var (
		pool *pgxpool.Pool
		opts httpapi.Options
	)
	if cfg.DB.URL == "" {
		// Without a database URL the process starts, but every authenticated
		// request answers 503 db_unavailable and /readyz stays 503.
		opts = HTTPOptions(cfg, logger, clock, unwiredAuthenticator{})
		opts.Health = httpapi.NewHealthHandler()
	} else {
		pool, err = postgres.NewPool(context.Background(), PostgresConfig(cfg))
		if err != nil {
			return nil, err
		}
		w, err := Wire(cfg, pool, logger, clock, cursors)
		if err != nil {
			pool.Close()
			return nil, err
		}
		opts = HTTPOptions(cfg, logger, clock, w.Authenticator)
		opts.Handlers, opts.Implemented = w.Handlers, w.Implemented
		opts.Health = httpapi.NewHealthHandler(func(ctx context.Context) error { return pool.Ping(ctx) })
	}
	handler, err := httpapi.NewServer(opts)
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		return nil, err
	}

	return &API{
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		log:  logger,
		pool: pool,
	}, nil
}

// Run serves until ctx is cancelled or SIGINT/SIGTERM arrives, then shuts down gracefully.
func (a *API) Run(ctx context.Context) error {
	defer a.Close()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		a.log.Info("http server listening", "addr", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	a.log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

// PostgresConfig maps the database settings of the process config to the
// postgres adapter config.
func PostgresConfig(cfg config.Config) postgres.Config {
	return postgres.Config{
		URL:              cfg.DB.URL,
		MaxConns:         cfg.DB.MaxConns,
		StatementTimeout: cfg.DB.StatementTimeout,
		IdleInTxTimeout:  cfg.DB.IdleInTxTimeout,
		ConnectTimeout:   cfg.DB.ConnectTimeout,
	}
}
