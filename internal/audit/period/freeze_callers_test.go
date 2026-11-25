package period

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProductionFreezeCallersPassZeroAt pins #3053 N-I: every production
// call to Store.Freeze passes a zero `at` (the literal time.Time{}), so
// frozen_at always comes from the database clock read after the tenant's
// evidence-freeze lock is held (#1742). An explicit `at` is for tests and
// replays only. Test files are exempt. A call is recognised by shape:
// a selector named Freeze with four arguments (Handler.Freeze takes two).
func TestProductionFreezeCallersPassZeroAt(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/audit/period/<file> -> repo root
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	var calls int
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel == ".git", rel == ".claude", rel == "node_modules",
				strings.HasPrefix(rel, "web/node_modules"):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Freeze" {
				return true
			}
			calls++
			if !isZeroTimeLit(call.Args[3]) {
				offenders = append(offenders, fset.Position(call.Pos()).String())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if calls == 0 {
		t.Fatal("found no production Store.Freeze call; the scan is not seeing the HTTP handler")
	}
	for _, o := range offenders {
		t.Errorf("%s: Store.Freeze must be called with time.Time{} so frozen_at comes from the DB clock after the lock", o)
	}
}

// isZeroTimeLit reports whether e is the literal time.Time{}.
func isZeroTimeLit(e ast.Expr) bool {
	lit, ok := e.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != 0 {
		return false
	}
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Time" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}
