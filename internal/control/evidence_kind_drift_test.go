package control_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/control"
)

// Slice 068 drift-guard.
//
// The evidence_kind identifier convention is `.v1`-suffixed kind +
// separate semver, per Plans/EVIDENCE_SDK.md §4.5. Three repo locations
// must agree on the kind set:
//
//  1. internal/api/schemaregistry/schemas/*/  — the bundled JSON Schemas
//     (each carries an x-evidence-kind extension key).
//  2. schemaregistry.DefaultSeed()           — the slim in-memory fallback.
//  3. controls/soc2/*/control.yaml           — every evidence_kind a SOC2
//     control bundle references.
//
// This file asserts mutual consistency. It is the regression net for the
// fresh-deploy phase-6 control-bundle upload bug: the SOC2 bundles used
// bare kind names (`osquery.host_posture`) while the registry held the
// `.v1` form, so a fresh-deploy bundle upload 400'd. Nothing asserted the
// invariant, so it was silent for ~14 slices.

// repoRoot walks up from this test file to the directory containing go.mod.
// Needed only to locate controls/soc2/ — the schema set is read via the
// embedded FS (schemaregistry.PlatformSchemasFS), no disk path required.
func repoRoot(t *testing.T) string {
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

// defaultSeedKinds returns the set of kind identifiers DefaultSeed registers.
func defaultSeedKinds() map[string]bool {
	out := map[string]bool{}
	for _, kv := range schemaregistry.DefaultSeed() {
		out[kv.Kind] = true
	}
	return out
}

// schemaFileKinds returns the set of x-evidence-kind identifiers across the
// bundled JSON Schemas, read via the same embedded-FS loader the atlas server
// boots with (schemaregistry.LoadPlatformSchemas) — not a hand-rolled parse.
func schemaFileKinds(t *testing.T) map[string]bool {
	t.Helper()
	schemas, err := schemaregistry.LoadPlatformSchemas(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("LoadPlatformSchemas: %v", err)
	}
	out := map[string]bool{}
	for _, s := range schemas {
		out[s.Kind] = true
	}
	return out
}

// soc2BundleKinds parses every controls/soc2/*/control.yaml and returns the
// set of evidence_kind identifiers the bundles reference (empty kinds skipped,
// matching ValidateEvidenceKinds semantics).
func soc2BundleKinds(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	forEachSOC2Bundle(t, func(_ string, b *control.Bundle) {
		for _, q := range b.Manifest.EvidenceQueries {
			if q.EvidenceKind != "" {
				out[q.EvidenceKind] = true
			}
		}
	})
	return out
}

// forEachSOC2Bundle parses every controls/soc2/*/control.yaml and invokes fn
// with the bundle's directory name and parsed Bundle.
func forEachSOC2Bundle(t *testing.T, fn func(name string, b *control.Bundle)) {
	t.Helper()
	soc2Dir := filepath.Join(repoRoot(t), "controls", "soc2")
	entries, err := os.ReadDir(soc2Dir)
	if err != nil {
		t.Fatalf("read controls/soc2: %v", err)
	}
	parsed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Every controls/soc2/ subdirectory is a control bundle — a parse
		// failure is a real defect, not a "skip me", so fail hard.
		b, perr := control.ParseDirectory(filepath.Join(soc2Dir, e.Name()))
		if perr != nil {
			t.Fatalf("parse bundle %s: %v", e.Name(), perr)
		}
		fn(e.Name(), b)
		parsed++
	}
	if parsed == 0 {
		t.Fatal("no SOC2 control bundles parsed — expected 50")
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEvidenceKindDrift_SchemaFilesMatchDefaultSeed asserts every bundled
// JSON Schema's x-evidence-kind is in DefaultSeed and vice versa.
func TestEvidenceKindDrift_SchemaFilesMatchDefaultSeed(t *testing.T) {
	t.Parallel()
	seed := defaultSeedKinds()
	files := schemaFileKinds(t)

	for k := range files {
		if !seed[k] {
			t.Errorf("schema file declares x-evidence-kind %q but DefaultSeed does not register it", k)
		}
	}
	for k := range seed {
		if !files[k] {
			t.Errorf("DefaultSeed registers %q but no schema file declares that x-evidence-kind", k)
		}
	}
}

// TestEvidenceKindDrift_EveryKindIsV1Suffixed asserts the canonical
// convention: every kind identifier in DefaultSeed and every schema-file
// x-evidence-kind ends with a `.v<major>` suffix (per EVIDENCE_SDK §4.5).
func TestEvidenceKindDrift_EveryKindIsV1Suffixed(t *testing.T) {
	t.Parallel()
	check := func(kinds map[string]bool, source string) {
		for k := range kinds {
			if !hasVersionSuffix(k) {
				t.Errorf("%s: kind %q is missing a .v<major> suffix (canonical convention per EVIDENCE_SDK.md §4.5)", source, k)
			}
		}
	}
	check(defaultSeedKinds(), "DefaultSeed")
	check(schemaFileKinds(t), "schemas/*/")
}

// TestEvidenceKindDrift_SOC2BundlesResolveInRegistry asserts every
// evidence_kind referenced by a SOC2 control bundle is registered in
// DefaultSeed. This is the direct regression net for the fresh-deploy
// phase-6 control-bundle upload bug.
func TestEvidenceKindDrift_SOC2BundlesResolveInRegistry(t *testing.T) {
	t.Parallel()
	seed := defaultSeedKinds()
	bundles := soc2BundleKinds(t)

	if len(bundles) == 0 {
		t.Fatal("no evidence_kind references found across controls/soc2/*/control.yaml — expected at least one")
	}
	for k := range bundles {
		if !seed[k] {
			t.Errorf("SOC2 control bundle references evidence_kind %q which is not registered in DefaultSeed (seed has: %v)", k, sortedKeys(seed))
		}
	}
}

// TestEvidenceKindDrift_SOC2BundlesPassRegistryValidation drives the actual
// bundle validation path (ValidateEvidenceKinds + registryKnowsKind) against
// an in-memory registry seeded from DefaultSeed — the same shape a fresh
// deploy's registry holds. This is the unit-level proof of AC-3: every SOC2
// bundle's evidence_kind resolves through the real validation code.
func TestEvidenceKindDrift_SOC2BundlesPassRegistryValidation(t *testing.T) {
	t.Parallel()
	reg := schemaregistry.New(schemaregistry.DefaultSeed())
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if err := b.ValidateEvidenceKinds(t.Context(), reg); err != nil {
			t.Errorf("bundle %s failed evidence_kind validation against the DefaultSeed registry: %v", name, err)
		}
	})
}

// hasVersionSuffix reports whether s ends with `.v<digits>`.
func hasVersionSuffix(s string) bool {
	dot := strings.LastIndex(s, ".v")
	if dot < 0 {
		return false
	}
	suffix := s[dot+2:]
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
