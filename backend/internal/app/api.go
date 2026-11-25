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

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/postgres"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

const shutdownTimeout = 15 * time.Second

// API is the assembled HTTP service.
type API struct {
	server *http.Server
	log    *slog.Logger
}

// BuildAPI wires the API process from the config.
func BuildAPI(cfg config.Config) (*API, error) {
	logger, err := cfg.NewLogger()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	// No readiness checks yet: /readyz stays 503 until the database is wired in.
	httpapi.NewHealthHandler().Register(mux)

	return &API{
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		log: logger,
	}, nil
}

// Run serves until ctx is cancelled or SIGINT/SIGTERM arrives, then shuts down gracefully.
func (a *API) Run(ctx context.Context) error {
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
