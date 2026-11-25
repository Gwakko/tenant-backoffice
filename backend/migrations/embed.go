// Package migrations embeds the goose SQL migrations into the binaries.
package migrations

import "embed"

// FS holds every migration file at its root.
//
//go:embed *.sql
var FS embed.FS
