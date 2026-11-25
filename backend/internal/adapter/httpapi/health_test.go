package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
)

func get(t *testing.T, h *httpapi.HealthHandler, path string) int {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

// Test-ID: T-HEALTH-01
func TestHealthz(t *testing.T) {
	require.Equal(t, http.StatusOK, get(t, httpapi.NewHealthHandler(), "/healthz"))
}

// Test-ID: T-HEALTH-02
func TestReadyz(t *testing.T) {
	require.Equal(t, http.StatusServiceUnavailable, get(t, httpapi.NewHealthHandler(), "/readyz"))

	bad := httpapi.NewHealthHandler(func(context.Context) error { return errors.New("down") })
	require.Equal(t, http.StatusServiceUnavailable, get(t, bad, "/readyz"))

	good := httpapi.NewHealthHandler(func(context.Context) error { return nil })
	require.Equal(t, http.StatusOK, get(t, good, "/readyz"))
}
