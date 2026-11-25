// Command api runs the HTTP API server.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "healthcheck" {
		return healthcheck(cfg.HTTPAddr)
	}
	api, err := app.BuildAPI(cfg)
	if err != nil {
		return err
	}
	return api.Run(context.Background())
}

// healthcheck probes /healthz of the local server. It exists because the
// distroless runtime image has no shell, curl or wget for a container check.
func healthcheck(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("healthcheck: bad TB_HTTP_ADDR %q: %w", addr, err)
	}
	if host == "" || strings.Trim(host, "0:.") == "" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
}
