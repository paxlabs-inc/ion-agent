// Package migrations exposes the versioned SQLite migrations embedded in the
// Ion binary.
package migrations

import "embed"

// Files contains every forward-only schema migration.
//
//go:embed *.sql
var Files embed.FS
