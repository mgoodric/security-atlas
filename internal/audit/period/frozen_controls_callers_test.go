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

// TestOnlyFreezeReadsLiveControlCatalog pins #3077 review N2 (#3066): the
// live-catalog query ListControlIDsForPeriodHash is called only by
// Store.Freeze, which records the list it hashes. Every replay and revision
// hash reads the recorded list (GetFrozenControlIDs); a second caller would
// reintroduce the live-catalog read #3066 removed. Test files are exempt.
func TestOnlyFreezeReadsLiveControlCatalog(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	var callers []string
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
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "ListControlIDsForPeriodHash" {
					callers = append(callers, rel+":"+funcName(fn))
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := "internal/audit/period/period.go:(*Store).Freeze"
	if len(callers) != 1 || callers[0] != want {
		t.Fatalf("ListControlIDsForPeriodHash callers = %v, want exactly [%s]", callers, want)
	}
}

// funcName renders a FuncDecl as Name or (*Recv).Name.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch r := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := r.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return "(" + r.Name + ")." + fn.Name.Name
	}
	return fn.Name.Name
}
