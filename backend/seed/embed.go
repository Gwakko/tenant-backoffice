// Package seed embeds the demo data set that cmd/seed loads.
package seed

import _ "embed" // required for the go:embed directive below

// Demo is backend/seed/demo.json: a demonstration tree, users, settings and a
// ledger history for local development and the end-to-end suite. It holds
// throwaway demo passwords only.
//
//go:embed demo.json
var Demo []byte
