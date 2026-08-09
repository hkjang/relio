package migrations

import "embed"

// Files contains every ordered PostgreSQL migration shipped with the binary.
//
//go:embed *.sql
var Files embed.FS
