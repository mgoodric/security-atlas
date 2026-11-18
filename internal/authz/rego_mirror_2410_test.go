package authz

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// #2410 — policies/authz/ is a reader-facing copy of the embedded
// rego_bundle/, which is what NewEngine and ReloadFromEmbedded load.
// The copy drifted once (the GET /v1/people admit existed only in the
// bundle). This test fails when the two directories differ, file for
// file and byte for byte. Fix by making both copies identical, e.g.
// `cp internal/authz/rego_bundle/*.rego policies/authz/`.
func TestRegoMirrorMatchesEmbeddedBundle(t *testing.T) {
	t.Parallel()

	bundle := readRegoDir(t, "rego_bundle")
	mirror := readRegoDir(t, filepath.Join("..", "..", "policies", "authz"))

	names := map[string]struct{}{}
	for n := range bundle {
		names[n] = struct{}{}
	}
	for n := range mirror {
		names[n] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, n := range sorted {
		b, inBundle := bundle[n]
		m, inMirror := mirror[n]
		switch {
		case !inMirror:
			t.Errorf("%s is in internal/authz/rego_bundle/ but not in policies/authz/", n)
		case !inBundle:
			t.Errorf("%s is in policies/authz/ but not in internal/authz/rego_bundle/", n)
		case !bytes.Equal(b, m):
			t.Errorf("%s differs between internal/authz/rego_bundle/ and policies/authz/", n)
		}
	}
}

func readRegoDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("%s/%s: unexpected subdirectory", dir, e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s/%s: %v", dir, e.Name(), err)
		}
		out[e.Name()] = data
	}
	return out
}
