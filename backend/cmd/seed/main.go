// Command seed loads the demonstration data set into the database.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
)

func main() {
	if err := app.RunSeed(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
