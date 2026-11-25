// Command migrate applies, reverts and checks database migrations.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
)

func main() {
	if err := app.RunMigrate(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
