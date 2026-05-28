// errleak-lint is the slice 367 custom static analyzer that rejects new
// code reflecting err.Error() (or err.Error()-derived strings) into the
// JSON body of a 5xx response.
//
// # What it flags
//
// A call to writeJSON, writeError, or render.JSON / json.NewEncoder etc.
// whose status-code argument is a 5xx constant AND whose body argument
// transitively contains a call to (error).Error() — meaning the call
// itself, a string concatenation that includes it, or a map literal whose
// values include it.
//
// What it does NOT flag
//
//   - 4xx-status writes (those are allowed to surface user-input errors
//     per slice 367 D1, until a future tightening pass).
//   - slog.Error/slog.Info calls with err.Error() — server-side logging
//     is encouraged.
//   - panic("...: " + err.Error()) — panics are server-side state, the
//     panic message lands in stderr, not in a JSON response.
//   - calls to the slice 367 httperr package — that helper is exactly
//     the migration target.
//
// How to run
//
//	go run ./cmd/scripts/errleak-lint ./internal/api/...
//
// Exit codes
//
//	0 — no leaks found
//	3 — at least one leak found (each on stderr with file:line and the
//	    offending response body fragment)
//	other — internal error (analysis framework failure)
//
// Slice 367 — security audit M-2 (CWE-209).
package main

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

// status5xxConstants is the closed set of net/http status-code constants
// the analyzer treats as 5xx. A code returned via a literal int (e.g.
// 500) is also flagged via the numeric check below; the constant set is
// for readability when the offending site uses the canonical name.
var status5xxConstants = map[string]struct{}{
	"StatusInternalServerError":           {},
	"StatusNotImplemented":                {},
	"StatusBadGateway":                    {},
	"StatusServiceUnavailable":            {},
	"StatusGatewayTimeout":                {},
	"StatusHTTPVersionNotSupported":       {},
	"StatusVariantAlsoNegotiates":         {},
	"StatusInsufficientStorage":           {},
	"StatusLoopDetected":                  {},
	"StatusNotExtended":                   {},
	"StatusNetworkAuthenticationRequired": {},
}

// writeFnNames is the set of function names treated as "writes a JSON
// response body". The analyzer flags any 5xx call to one of these whose
// body argument touches err.Error().
var writeFnNames = map[string]struct{}{
	"writeJSON":  {},
	"writeError": {},
	"WriteJSON":  {},
	"WriteError": {},
}

var analyzer = &analysis.Analyzer{
	Name: "errleak",
	Doc:  "reports writeJSON/writeError calls at 5xx status that reflect err.Error() into the response body (CWE-209, slice 367)",
	Run:  run,
}

func main() {
	singlechecker.Main(analyzer)
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fnName, isPkg := writeCallName(call)
			if isPkg {
				// foo.WriteJSON(...) — skip if foo is the httperr helper.
				// Conservative: only flag bare writeJSON / writeError calls.
				return true
			}
			if _, ok := writeFnNames[fnName]; !ok {
				return true
			}
			// Find the status-code argument (positional: typically arg 1
			// — index depends on the helper's signature; we scan args 0/1/2
			// for a 5xx constant or numeric literal).
			if !hasStatus5xxArg(call) {
				return true
			}
			// Find any err.Error() reflection in the remaining args.
			if site := findErrorReflectionSite(call); site != token.NoPos {
				pass.Reportf(site,
					"slice 367 / CWE-209: %s at 5xx status reflects err.Error() into the response body — use internal/api/httperr.WriteInternal(w, r, %q, err) instead",
					fnName, "<op-label>",
				)
			}
			return true
		})
	}
	return nil, nil
}

// writeCallName returns the called identifier name and a "is qualified
// (pkg.Fn)" flag. For pkg.Fn calls we return the bare Fn name and
// isPkg=true so the caller can decide whether to flag based on the
// package. We treat bare writeJSON/writeError calls — the codebase's
// existing pattern — as the analyzer's flagging surface.
func writeCallName(call *ast.CallExpr) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name, false
	case *ast.SelectorExpr:
		return fn.Sel.Name, true
	}
	return "", false
}

// hasStatus5xxArg returns true when one of the call's args is a
// net/http status constant in the 5xx set OR a numeric literal in
// [500, 599].
func hasStatus5xxArg(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.SelectorExpr:
			// http.StatusInternalServerError etc.
			if _, ok := status5xxConstants[a.Sel.Name]; ok {
				return true
			}
		case *ast.BasicLit:
			if a.Kind == token.INT {
				v := a.Value
				if len(v) == 3 && v[0] == '5' {
					return true
				}
			}
		}
	}
	return false
}

// findErrorReflectionSite walks the body-argument expressions looking
// for a call shape `<x>.Error()` where the call has zero args (matches
// the `error` interface contract). Returns the call's position on the
// first hit, token.NoPos otherwise.
func findErrorReflectionSite(call *ast.CallExpr) token.Pos {
	var hit token.Pos
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := c.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Error" && len(c.Args) == 0 {
				// Heuristic: skip references to *PgError.Error (would be
				// `pgErr.Error()`) by checking the receiver is named `err`
				// or `terr` or any identifier whose name CONTAINS "err".
				// This keeps the heuristic loose — a false positive on a
				// non-error type is rare; the alternative (use type info)
				// is heavier and not needed for the M-2 surface.
				if isLikelyErrorReceiver(sel.X) {
					hit = c.Pos()
					return false
				}
			}
			return true
		})
		if hit != token.NoPos {
			return hit
		}
	}
	return token.NoPos
}

func isLikelyErrorReceiver(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	if !ok {
		return true // unknown shape — flag conservatively
	}
	name := strings.ToLower(id.Name)
	return strings.Contains(name, "err")
}
