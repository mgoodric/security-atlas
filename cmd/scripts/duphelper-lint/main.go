// duphelper-lint is the slice 369 custom static analyzer that rejects new
// per-package declarations of the response helpers writeJSON, writeError,
// and writeServerErr inside internal/api/*.
//
// # Background
//
// Slice 328's code-review audit (finding H-1) surfaced 103 byte-identical
// declarations of these three helpers spread across 50+ internal/api/*
// packages. Slice 369 consolidated them into:
//
//   - internal/api/httpresp.WriteJSON / WriteError  (2xx + 4xx JSON)
//   - internal/api/httperr.WriteInternal            (5xx, slice 367)
//
// This analyzer prevents the duplication from creeping back. Any handler
// needing to emit a JSON response calls the shared helper; declaring a new
// package-local writeJSON/writeError/writeServerErr is the exact regression
// this guard rejects.
//
// # What it flags
//
// A top-level (package-scoped) FuncDecl whose name is writeJSON, writeError,
// or writeServerErr. Methods (FuncDecls with a receiver) are NOT flagged —
// a type may legitimately carry such a method; the duplication problem was
// specifically free functions.
//
// # Scope
//
// The analyzer is invoked against ./internal/api/... only (see the justfile
// `lint-duphelper` target), so the path scoping is enforced by the
// invocation target — mirroring how slice 367's errleak-lint is wired.
// The two shared packages themselves (httpresp, httperr) export Capitalised
// names (WriteJSON, WriteError, WriteInternal) and are not matched.
//
// # How to run
//
//	go run ./cmd/scripts/duphelper-lint ./internal/api/...
//
// # Exit codes
//
//	0     — no duplicate helper declarations found
//	3     — at least one found (each on stderr with file:line)
//	other — internal error (analysis framework failure)
//
// Slice 369 — code-review audit H-1 (helper consolidation).
package main

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

// bannedHelperNames is the closed set of free-function names that slice 369
// consolidated into internal/api/httpresp + internal/api/httperr. A new
// package-local declaration of any of these is a regression.
var bannedHelperNames = map[string]string{
	"writeJSON":      "internal/api/httpresp.WriteJSON",
	"writeError":     "internal/api/httpresp.WriteError (4xx) or internal/api/httperr.WriteInternal (5xx)",
	"writeServerErr": "internal/api/httperr.WriteInternal",
}

var analyzer = &analysis.Analyzer{
	Name: "duphelper",
	Doc:  "reports package-local writeJSON/writeError/writeServerErr declarations in internal/api/* that duplicate the shared httpresp/httperr helpers (slice 369)",
	Run:  run,
}

func main() {
	singlechecker.Main(analyzer)
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// Skip methods — only free functions are the duplication
			// surface. A method named writeJSON on some type is rare and
			// not what H-1 flagged.
			if fn.Recv != nil {
				continue
			}
			if fn.Name == nil {
				continue
			}
			replacement, banned := bannedHelperNames[fn.Name.Name]
			if !banned {
				continue
			}
			pass.Reportf(fn.Name.Pos(),
				"slice 369 / H-1: package-local %s declaration duplicates the shared response helper — call %s instead and delete this function",
				fn.Name.Name, replacement,
			)
		}
	}
	return nil, nil
}
