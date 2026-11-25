// Command api runs the HTTP API server.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	api, err := app.BuildAPI(cfg)
	if err != nil {
		return err
	}
	return api.Run(context.Background())
}
