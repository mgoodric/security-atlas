// Slice 140 — atlas-openapi CLI.
//
// Emits the OpenAPI 3.1 spec for the security-atlas REST surface to
// the file path given by --out. Reads the canonical route inventory
// from internal/api/openapi.RouteSpecs. Pure Go, no external deps
// beyond the standard library + internal packages — no DB, no
// network, no flaky inputs. Two back-to-back runs against the same
// RouteSpecs produce byte-identical output (load-bearing for the
// BLOCKING `openapi-drift-check` CI guard — slice 140 D3).
//
// Usage (from `just openapi-generate`):
//
//	go run ./cmd/atlas-openapi --out docs/openapi.yaml
//
// Exit codes:
//
//	0 — spec generated and written successfully
//	1 — IO error writing the output file
//	2 — invalid flag combination
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/mgoodric/security-atlas/internal/api/openapi"
)

func main() {
	out := flag.String("out", "docs/openapi.yaml", "path to write the generated OpenAPI spec")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "atlas-openapi: --out is required (path to write the spec)")
		os.Exit(2)
	}

	// Generate into a buffer first; only write to disk on full success.
	// This keeps the on-disk file consistent (no partial writes).
	// 0644 matches the rest of the repo's tracked files.
	//
	// Slice 140 P0-A8: single source of truth file. The public Redoc
	// UI filters `x-internal: true` operations at render time via a
	// mkdocs hook (see docs-site/hooks/filter_openapi_internal.py),
	// NOT via a sibling `.public.yaml` autogen file.
	var buf bytes.Buffer
	if err := openapi.Generate(&buf, openapi.RouteSpecs); err != nil {
		fmt.Fprintf(os.Stderr, "atlas-openapi: generate: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "atlas-openapi: write %s: %v\n", *out, err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "atlas-openapi: wrote %d routes to %s (%d bytes)\n",
		len(openapi.RouteSpecs), *out, buf.Len())
}
