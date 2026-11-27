package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Options assemble the HTTP server.
type Options struct {
	Log           *slog.Logger
	Clock         port.Clock
	Authenticator Authenticator
	Limiter       Limiter
	Health        *HealthHandler
	// Handlers implements the operations. Operations listed in Implemented are
	// routed to it; every other operation answers 501 not_implemented. A nil
	// Handlers means nothing is implemented yet.
	Handlers    gen.StrictServerInterface
	Implemented map[string]bool

	MaxBodyBytes      int64
	TrustForwardedFor bool
	Rates             RateBudgets
	// NewRequestID generates ids for requests without a valid incoming one.
	NewRequestID func() string
}

// placeholder stands in for Handlers; it is never called because the
// not-implemented middleware answers first.
type placeholder struct{ gen.StrictServerInterface }

// NewServer builds the handler of the whole service: /healthz, /readyz and
// /api/v1 over the generated strict server, behind the middleware chain
// request id, access log, recover, routing, body limit, content type, authn,
// rate limit, idempotency key, body shape.
func NewServer(o Options) (http.Handler, error) {
	switch {
	case o.Log == nil || o.Clock == nil:
		return nil, errors.New("httpapi: logger and clock are required")
	case o.Authenticator == nil || o.Limiter == nil || o.Health == nil:
		return nil, errors.New("httpapi: authenticator, limiter and health handler are required")
	case o.MaxBodyBytes < 1:
		return nil, errors.New("httpapi: body limit must be positive")
	}
	newID := o.NewRequestID
	if newID == nil {
		newID = uuid.NewString
	}

	mux := http.NewServeMux()
	o.Health.Register(mux)
	mux.Handle("/", notFoundHandler(o.Log))

	var ssi gen.StrictServerInterface = placeholder{}
	if o.Handlers != nil {
		ssi = o.Handlers
	}
	implemented := o.Implemented
	notImpl := func(next gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		if o.Handlers != nil && implemented[operationID] {
			return next
		}
		return func(_ context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
			return nil, errNotImplemented
		}
	}
	opts := problemWriter(o.Log)
	strict := gen.NewStrictHandlerWithOptions(ssi, []gen.StrictMiddlewareFunc{notImpl}, opts)
	gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:          apiPrefix,
		BaseRouter:       apiRouter{mux},
		ErrorHandlerFunc: opts.RequestErrorHandlerFunc,
	})

	return chain(mux,
		requestIDMiddleware(newID),
		accessLog(o.Log, o.Clock),
		recoverer(o.Log),
		routing(mux),
		limitBody(o.MaxBodyBytes, o.Log),
		requireJSON(o.Log),
		authn(o.Authenticator, o.Log),
		rateLimit(o.Limiter, o.Rates, o.TrustForwardedFor, o.Log),
		idempotencyKey(o.Log),
		strictBody(o.Log),
	), nil
}

// apiRouter registers the generated routes except the health probes, which the
// service serves itself at /healthz and /readyz outside the versioned base.
type apiRouter struct{ *http.ServeMux }

func (r apiRouter) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	if strings.HasSuffix(pattern, "/healthz") || strings.HasSuffix(pattern, "/readyz") {
		return
	}
	r.ServeMux.HandleFunc(pattern, h)
}
