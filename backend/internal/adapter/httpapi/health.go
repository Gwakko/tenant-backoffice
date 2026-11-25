// Package httpapi contains the HTTP transport adapter.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

// ReadyCheck reports whether a dependency is ready to serve traffic.
type ReadyCheck func(ctx context.Context) error

// HealthHandler serves liveness and readiness probes.
type HealthHandler struct {
	checks []ReadyCheck
}

// NewHealthHandler builds a handler. Without checks the service is never ready,
// because readiness requires at least one dependency check (the database).
func NewHealthHandler(checks ...ReadyCheck) *HealthHandler {
	return &HealthHandler{checks: checks}
}

// Register mounts /healthz and /readyz on the mux.
func (h *HealthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
}

func (h *HealthHandler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

func (h *HealthHandler) readyz(w http.ResponseWriter, r *http.Request) {
	if len(h.checks) == 0 {
		writeStatus(w, http.StatusServiceUnavailable, "no readiness checks configured")
		return
	}
	for _, check := range h.checks {
		if err := check(r.Context()); err != nil {
			writeStatus(w, http.StatusServiceUnavailable, "not ready")
			return
		}
	}
	writeStatus(w, http.StatusOK, "ready")
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
