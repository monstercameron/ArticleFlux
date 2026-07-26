// Package migrations embeds the schema files.
//
// It exists as its own package because go:embed cannot reach outside its own
// directory tree — `//go:embed ../../migrations/*.sql` is a compile error, not a
// path that resolves. Keeping the SQL beside the embed directive also means the
// files stay visible as SQL to anyone reading the repo, rather than becoming Go
// string literals nobody can diff.
package migrations

import "embed"

// FS holds every NNNN_name.sql migration, applied in version order by
// internal/store.
//
//go:embed *.sql
var FS embed.FS
