// Package migrations embeds the SQL schema files.
package migrations

import "embed"

// FS holds the numbered *.sql migrations, applied in name order.
//
//go:embed *.sql
var FS embed.FS
