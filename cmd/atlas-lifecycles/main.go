// atlas-lifecycles exports every declared lifecycle transition table
// (#2146) as JSON, one document per lifecycle, to --out
// (docs/lifecycles/ by default). The in-app help's diagram check (#2148)
// compares each lifecycle diagram with its document. Pure Go: no
// database, no network; two runs produce byte-identical files.
//
// Usage (from `just lifecycles-export`):
//
//	go run ./cmd/atlas-lifecycles --out docs/lifecycles
//
// TestExport_MatchesCommitted fails when the committed documents and the
// tables disagree; re-run the export to fix it.
//
// Exit codes:
//
//	0 — every document written
//	1 — a table is invalid, or an IO error
//	2 — invalid flags
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/lifecycle"
	"github.com/mgoodric/security-atlas/internal/policy"
)

// tables lists every declared lifecycle. A new table is added here.
func tables() []lifecycle.Table {
	return []lifecycle.Table{
		actionplan.Lifecycle,
		exception.Lifecycle,
		policy.Lifecycle,
		frameworkscope.Lifecycle,
		period.Lifecycle,
	}
}

// render returns each lifecycle's document keyed by file name.
func render(ts []lifecycle.Table) (map[string][]byte, error) {
	out := make(map[string][]byte, len(ts))
	for _, t := range ts {
		if err := t.Validate(); err != nil {
			return nil, err
		}
		name := t.Name + ".json"
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("lifecycle %s is declared twice", t.Name)
		}
		b, err := t.JSON()
		if err != nil {
			return nil, fmt.Errorf("lifecycle %s: %w", t.Name, err)
		}
		out[name] = b
	}
	return out, nil
}

func main() {
	dir := flag.String("out", "docs/lifecycles", "directory to write one <lifecycle>.json per lifecycle")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "atlas-lifecycles: --out is required")
		os.Exit(2)
	}
	docs, err := render(tables())
	if err != nil {
		fmt.Fprintf(os.Stderr, "atlas-lifecycles: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "atlas-lifecycles: %v\n", err)
		os.Exit(1)
	}
	for name, b := range docs {
		if err := os.WriteFile(filepath.Join(*dir, name), b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "atlas-lifecycles: write %s: %v\n", name, err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "atlas-lifecycles: wrote %d lifecycles to %s\n", len(docs), *dir)
}
