package metrics_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

// realCatalogFS returns an fs.FS rooted at the repo's catalogs/metrics/
// directory so the loader can exercise the actual shipped YAML. Walks up
// from this test file to find the repo root, then loads every *.yaml file
// under catalogs/metrics/ into an in-memory fstest.MapFS (so the loader's
// fs.WalkDir semantics match production without needing os.DirFS quirks
// across platforms).
func realCatalogFS(t *testing.T) fs.FS {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "catalogs", "metrics")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return loadDirInto(t, candidate)
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find catalogs/metrics/ walking up from " + thisFile)
	return nil
}

func loadDirInto(t *testing.T, dir string) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = &fstest.MapFile{Data: body}
		return nil
	})
	if err != nil {
		t.Fatalf("loadDirInto: %v", err)
	}
	return out
}
