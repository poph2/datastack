// Package migrations embeds the Customer Service's goose SQL migrations into
// the binary, so `customer migrate up` needs nothing but a database URL.
package migrations

import "embed"

// FS holds the NNNNN_name.sql migration files at its root.
//
//go:embed *.sql
var FS embed.FS
