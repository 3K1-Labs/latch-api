// Package migrations embeds the SQL migrations so the server binary can apply
// them at startup without the files being present on disk.
package migrations

import "embed"

// FS holds every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
