package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/ratelimit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// SystemClock is the wall clock in UTC.
type SystemClock struct{}

// Now implements port.Clock.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// HTTPOptions maps the config to the transport options. The collaborators that
// need the database (authenticator, handlers) are passed by the caller.
func HTTPOptions(cfg config.Config, log *slog.Logger, clock port.Clock, authn httpapi.Authenticator) httpapi.Options {
	return httpapi.Options{
		Log:               log,
		Clock:             clock,
		Authenticator:     authn,
		Limiter:           ratelimit.NewMemory(clock, cfg.Rate.MaxKeys),
		MaxBodyBytes:      cfg.HTTP.MaxBodyBytes,
		TrustForwardedFor: cfg.HTTP.TrustForwardedFor,
		Rates: httpapi.RateBudgets{
			LoginPair: cfg.Rate.LoginPairPerMin, LoginIP: cfg.Rate.LoginIPPerMin,
			RefreshIP: cfg.Rate.RefreshIPPerMin, Mutation: cfg.Rate.MutationPerMin, Read: cfg.Rate.ReadPerMin,
		},
	}
}

// CursorCodec builds the pagination cursor codec from the config. Without
// TB_CURSOR_KEY a random key is generated for this process: cursors then stop
// working after a restart, but nothing is ever signed with a guessable key.
func CursorCodec(cfg config.Config, clock port.Clock, log *slog.Logger) (*httpapi.CursorCodec, error) {
	keys, err := cfg.HTTP.CursorKeys()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("cursor key: %w", err)
		}
		keys = [][]byte{key}
		log.Warn("TB_CURSOR_KEY is not set: using a random per-process cursor key")
	}
	return httpapi.NewCursorCodec(keys, cfg.HTTP.CursorTTL, clock)
}

// unwiredAuthenticator answers every token check with db_unavailable until the
// database is wired into BuildAPI together with the handlers (M4.3).
type unwiredAuthenticator struct{}

func (unwiredAuthenticator) Execute(context.Context, auth.AuthenticateInput) (auth.AuthenticateOutput, error) {
	return auth.AuthenticateOutput{}, apperr.UnavailableError()
}
