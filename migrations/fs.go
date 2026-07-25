package migrations

import "embed"

// FS contains versioned *.up.sql migration files.
//
//go:embed *.up.sql
var FS embed.FS
