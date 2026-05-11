package schemaregistry

import (
	"embed"
	"io/fs"
)

// embeddedSchemas holds the platform-bundled JSON Schemas at compile time.
// The `all:` prefix is required so subdirectories beginning with a digit
// (e.g. `1password.org_policy`) are walked — without it Go's embed pattern
// skips them silently.
//
// Canonical authoring location: internal/api/schemaregistry/schemas/.
// The top-level `schemas/` directory is a discovery breadcrumb that points
// here; embedding cannot traverse upward out of the package.
//
//go:embed all:schemas
var embeddedSchemas embed.FS

// PlatformSchemasFS returns the embedded schemas filesystem rooted at
// `schemas/` (i.e., the caller iterates kinds directly without stripping
// a `schemas/` prefix).
func PlatformSchemasFS() fs.FS {
	sub, err := fs.Sub(embeddedSchemas, "schemas")
	if err != nil {
		// Constructed at compile time; this path is unreachable in practice.
		panic("schemaregistry: embed fs.Sub failed: " + err.Error())
	}
	return sub
}
