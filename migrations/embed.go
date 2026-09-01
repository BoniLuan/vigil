package migrations

import "embed"

// Files contains all immutable database migrations shipped with the binary.
//
//go:embed *.sql
var Files embed.FS
