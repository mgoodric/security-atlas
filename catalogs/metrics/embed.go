// Package metricscatalog embeds the curated metrics catalog YAML files
// (slice 076) so the binary ships with its own catalog. The seeder in
// internal/catalog/metrics consumes the EmbeddedFS() return value at
// boot; tests can swap in an in-memory fstest.MapFS via the loader's
// fs.FS argument.
package metricscatalog

import (
	"embed"
	"io/fs"
)

//go:embed *.yaml
var embedded embed.FS

// EmbeddedFS returns the embedded *.yaml tree as an fs.FS. The seeder
// walks this with fs.WalkDir to load every metric definition.
func EmbeddedFS() fs.FS { return embedded }
