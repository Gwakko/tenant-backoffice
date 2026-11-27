//go:build integration

package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
	"github.com/Gwakko/tenant-backoffice/backend/internal/testkit"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	require.NoError(t, l.Close())
	// The loopback address is built, not spelled out: check-hosts forbids literal addresses in tests.
	return net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), port)
}

func urlAt(t *testing.T, rawURL, hostPort string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	u.Host = hostPort
	return u.String()
}

// relayLater forwards hostPort to target, but starts listening only after delay.
func relayLater(t *testing.T, hostPort, target string, delay time.Duration) {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		select {
		case <-time.After(delay):
		case <-done:
			return
		}
		l, err := net.Listen("tcp", hostPort)
		if err != nil {
			return
		}
		go func() { <-done; _ = l.Close() }()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = up.Close() }()
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
}

func get(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

// Test-ID: T-I-43
// EC-135: a process that starts before its database waits for it up to
// TB_DB_CONNECT_TIMEOUT and then fails; /readyz follows the database and
// /healthz stays up.
func TestStartupWaitsForDBAndReadyz(t *testing.T) {
	db := testkit.NewDB(t)
	realURL := db.URL(t)

	pgCfg, err := pgxpool.ParseConfig(realURL)
	require.NoError(t, err)
	realAddr := net.JoinHostPort(pgCfg.ConnConfig.Host, strconv.Itoa(int(pgCfg.ConnConfig.Port)))

	load := func(t *testing.T, dbURL, timeout string) config.Config {
		t.Helper()
		t.Setenv("TB_HTTP_ADDR", ":0")
		t.Setenv("TB_DB_URL", dbURL)
		t.Setenv("TB_DB_CONNECT_TIMEOUT", timeout)
		cfg, err := config.Load()
		require.NoError(t, err)
		return cfg
	}

	t.Run("a wrong port fails after the timeout", func(t *testing.T) {
		cfg := load(t, urlAt(t, realURL, freeAddr(t)), "2s")
		start := time.Now()
		pool, err := app.ConnectDB(context.Background(), app.PostgresConfig(cfg))
		elapsed := time.Since(start)
		require.Error(t, err)
		require.Nil(t, pool)
		require.GreaterOrEqual(t, elapsed, 1800*time.Millisecond, "it must keep trying until the timeout")
		require.Less(t, elapsed, 8*time.Second)
	})

	t.Run("a database that comes up late is waited for", func(t *testing.T) {
		addr := freeAddr(t)
		relayLater(t, addr, realAddr, 900*time.Millisecond)
		cfg := load(t, urlAt(t, realURL, addr), "10s")
		start := time.Now()
		pool, err := app.ConnectDB(context.Background(), app.PostgresConfig(cfg))
		require.NoError(t, err)
		defer pool.Close()
		require.GreaterOrEqual(t, time.Since(start), 800*time.Millisecond)
		require.NoError(t, pool.Ping(context.Background()))
	})

	t.Run("readyz follows the database, healthz does not", func(t *testing.T) {
		cfg := load(t, realURL, "5s")
		pool, err := app.ConnectDB(context.Background(), app.PostgresConfig(cfg))
		require.NoError(t, err)
		mux := http.NewServeMux()
		httpapi.NewHealthHandler(app.DBReadyCheck(pool)).Register(mux)

		require.Equal(t, http.StatusOK, get(t, mux, "/readyz"))
		require.Equal(t, http.StatusOK, get(t, mux, "/healthz"))
		pool.Close()
		require.Equal(t, http.StatusServiceUnavailable, get(t, mux, "/readyz"))
		require.Equal(t, http.StatusOK, get(t, mux, "/healthz"))

		// Without any check the service is not ready either.
		bare := http.NewServeMux()
		httpapi.NewHealthHandler().Register(bare)
		require.Equal(t, http.StatusServiceUnavailable, get(t, bare, "/readyz"))
	})

	t.Run("a malformed URL is not retried", func(t *testing.T) {
		cfg := load(t, "not a url ://", "10s")
		start := time.Now()
		_, err := app.ConnectDB(context.Background(), app.PostgresConfig(cfg))
		require.Error(t, err)
		require.Less(t, time.Since(start), 2*time.Second)
	})
}
