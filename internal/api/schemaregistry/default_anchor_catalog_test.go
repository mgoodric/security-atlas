package schemaregistry

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// Slice 654 — x-default-scf-anchors catalog-existence guard.
//
// Every bundled evidence-kind schema may carry an `x-default-scf-anchors`
// hint: the default control-mapping suggestion an operator approves once
// (embed.go parses it into PlatformSchema.DefaultSCFAnchors; service.go
// stores it as evidence_kind_schemas.default_scf_anchors). Nothing on `main`
// validated that those anchors resolve to a real anchor in the bundled SCF
// catalog. A schema could ship a hint pointing at an anchor absent from the
// deployment's catalog, silently — the suggested mapping then resolves to
// nothing and the evidence-kind appears to map to a control the catalog does
// not contain.
//
// This guard closes that mechanical-existence gap. It is the EXISTENCE half
// of the load-bearing check; the maintainer's manual review (OQ #16/#17,
// resolved 2026-05-20) owns the ACCURACY half (is this the RIGHT anchor for
// this evidence kind). The two are complementary: this guard catches the
// dangling / non-existent class the manual checkpoint demonstrably missed
// (18 schemas shipped with dangling anchors on `main`). See
// docs/audit-log/654-schema-default-anchor-catalog-validation-decisions.md.
//
// It is the slice-068 anchor-drift guard's SIBLING on a different surface:
// slice 068 validates control-bundle anchors; this validates schema
// x-default-scf-anchors. The two do not overlap.

// bundledCatalogAnchors loads the bundled SCF catalog fixture
// (migrations/fixtures/scf-sample.json) via the canonical scfimport.Load
// parser — the same shape the seed/import path consumes — and returns the
// set of anchor codes (scf_id) it contains. This is the authoritative
// "present anchor" set the guard validates against (AC-3: the guard's catalog
// source is the bundled seed fixture).
func bundledCatalogAnchors(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(repoRootForAnchorGuard(t), "migrations", "fixtures", "scf-sample.json")
	cat, err := scfimport.Load(path)
	if err != nil {
		t.Fatalf("load bundled SCF catalog fixture %s: %v", path, err)
	}
	present := make(map[string]bool, len(cat.Controls))
	for _, c := range cat.Controls {
		present[c.SCFID] = true
	}
	if len(present) == 0 {
		t.Fatal("bundled SCF catalog fixture parsed zero anchors")
	}
	return present
}

// anchorExistsInCatalog is the small, testable helper at the heart of the
// guard: does a single anchor code resolve to a present catalog anchor.
func anchorExistsInCatalog(anchor string, catalog map[string]bool) bool {
	return catalog[anchor]
}

// danglingAnchors returns the subset of `anchors` that do NOT resolve to a
// present catalog anchor, in sorted order for stable assertion messages.
func danglingAnchors(anchors []string, catalog map[string]bool) []string {
	var out []string
	for _, a := range anchors {
		if !anchorExistsInCatalog(a, catalog) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// repoRootForAnchorGuard walks up from this test file to the directory
// containing go.mod, mirroring internal/control/evidence_kind_drift_test.go's
// repoRoot helper. Needed only to locate the migrations/fixtures fixture; the
// schema set is read via the embedded FS (PlatformSchemasFS), no disk path.
func repoRootForAnchorGuard(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	for dir := filepath.Dir(thisFile); ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod) walking up from test file")
		}
		dir = parent
	}
}

// TestDefaultSCFAnchors_ResolveInBundledCatalog is the AC-1 guard: every
// embedded schema's x-default-scf-anchors must resolve to an anchor present
// in the bundled SCF catalog fixture. It reads the schema set via the same
// embedded-FS loader the atlas server boots with (LoadPlatformSchemas), not a
// hand-rolled parse, so it tracks the real registration path.
func TestDefaultSCFAnchors_ResolveInBundledCatalog(t *testing.T) {
	t.Parallel()
	catalog := bundledCatalogAnchors(t)

	schemas, err := LoadPlatformSchemas(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("LoadPlatformSchemas: %v", err)
	}
	if len(schemas) == 0 {
		t.Fatal("LoadPlatformSchemas returned zero schemas")
	}

	checked := 0
	for _, s := range schemas {
		if len(s.DefaultSCFAnchors) == 0 {
			continue
		}
		checked++
		if bad := danglingAnchors(s.DefaultSCFAnchors, catalog); len(bad) > 0 {
			t.Errorf("schema %s/%s declares x-default-scf-anchors %v but %v are absent from the bundled SCF catalog (migrations/fixtures/scf-sample.json) — remap to a present, semantically-closest anchor (see slice 654 decisions log)",
				s.Kind, s.Semver, s.DefaultSCFAnchors, bad)
		}
	}
	if checked == 0 {
		t.Fatal("no schema with x-default-scf-anchors was checked — the guard would be a no-op (expected several)")
	}
	t.Logf("validated x-default-scf-anchors across %d schema(s) carrying anchor hints", checked)
}

// TestAnchorExistsInCatalog_RejectsDanglingAnchor is the AC-1 negative
// sub-test: it proves the guard's existence check actually rejects a
// deliberately-dangling anchor. Without it, a guard that always returned true
// would pass the positive test vacuously.
func TestAnchorExistsInCatalog_RejectsDanglingAnchor(t *testing.T) {
	t.Parallel()
	catalog := bundledCatalogAnchors(t)

	cases := []struct {
		name   string
		anchor string
		want   bool
	}{
		// Present anchors (sanity that the helper resolves real ones).
		{"present_MON-01", "MON-01", true},
		{"present_IRO-09", "IRO-09", true},
		{"present_CFG-02", "CFG-02", true},
		// Deliberately-dangling fake anchor — MUST be rejected.
		{"dangling_ZZZ-99", "ZZZ-99", false},
		// The historical real-but-absent anchors this slice remapped away.
		{"dangling_MON-02", "MON-02", false},
		{"dangling_IRO-02", "IRO-02", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := anchorExistsInCatalog(tc.anchor, catalog); got != tc.want {
				t.Errorf("anchorExistsInCatalog(%q) = %v; want %v", tc.anchor, got, tc.want)
			}
		})
	}

	// The guard must FAIL when fed a dangling anchor: assert danglingAnchors
	// flags the fake one in a mixed list (the negative-fixture proof).
	mixed := []string{"MON-01", "ZZZ-99", "IRO-09"}
	bad := danglingAnchors(mixed, catalog)
	if len(bad) != 1 || bad[0] != "ZZZ-99" {
		t.Fatalf("danglingAnchors(%v) = %v; want exactly [ZZZ-99] — the guard must catch a deliberately-dangling anchor", mixed, bad)
	}
}
