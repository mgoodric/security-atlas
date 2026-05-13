package featureflag

import (
	"strings"
	"testing"
)

// TestSeedExcludesSpineNamespaces is the anti-criterion P0 enforcer.
// Every key in Seed MUST NOT fall under any SpineForbiddenPrefix. Adding
// a key that disables a spine surface (RLS, tenancy, auth, schema
// registry, scope, evidence ledger, framework crosswalks, controls
// spine) is rejected here so a future contributor cannot accidentally
// undermine constitutional invariants.
func TestSeedExcludesSpineNamespaces(t *testing.T) {
	for _, d := range Seed {
		if IsSpineForbidden(d.Key) {
			t.Errorf("seed key %q matches a SpineForbiddenPrefix -- spine flags MUST NOT be toggleable", d.Key)
		}
	}
}

// TestSeedAllKeysUnique guards against accidental duplicate keys -- a
// duplicate would cause the upsert PK conflict to overwrite the wrong
// Default at first toggle.
func TestSeedAllKeysUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(Seed))
	for _, d := range Seed {
		if _, ok := seen[d.Key]; ok {
			t.Errorf("seed contains duplicate key %q", d.Key)
		}
		seen[d.Key] = struct{}{}
	}
}

// TestSeedKeysAreSnakeCaseNamespaced asserts every key follows the
// `namespace.feature` convention (lowercase, snake_case, single dot
// separator allowed) so flag identifiers are predictable from category.
func TestSeedKeysAreSnakeCaseNamespaced(t *testing.T) {
	for _, d := range Seed {
		if d.Key != strings.ToLower(d.Key) {
			t.Errorf("seed key %q is not lowercase", d.Key)
		}
		if !strings.Contains(d.Key, ".") {
			t.Errorf("seed key %q lacks a namespace separator (expected `namespace.feature`)", d.Key)
		}
		// snake_case + dots only -- no spaces, no slashes.
		for _, r := range d.Key {
			if r == '.' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				continue
			}
			t.Errorf("seed key %q contains invalid character %q", d.Key, r)
		}
	}
}

// TestSeedCategoriesMatchSchemaEnum asserts every Seed.Category is one
// of the 9 enum entries CHECKed at the DB layer. Drift between this list
// and the migration's CHECK constraint would surface as a constraint
// violation at first toggle; this unit test catches it pre-migration.
func TestSeedCategoriesMatchSchemaEnum(t *testing.T) {
	allowed := map[string]struct{}{
		"core": {}, "risk": {}, "vendor": {}, "policy": {}, "controls": {},
		"audit": {}, "evidence": {}, "board": {}, "integrations": {},
	}
	for _, d := range Seed {
		if _, ok := allowed[d.Category]; !ok {
			t.Errorf("seed key %q has invalid category %q (must be one of: core/risk/vendor/policy/controls/audit/evidence/board/integrations)", d.Key, d.Category)
		}
	}
}

// TestSeedContainsExpectedKeys is the inverse of the spine-exclusion
// test: the 12 documented seed keys MUST be present. A missing key
// surfaces as a regression before the integration tests catch it.
func TestSeedContainsExpectedKeys(t *testing.T) {
	expected := []string{
		"risk.enabled", "risk.themes", "risk.hierarchy",
		"vendor.enabled",
		"policy.enabled", "policy.acknowledgments",
		"controls.bundles", "exceptions.enabled",
		"audit.workflow",
		"oscal.export", "board.reporting",
		"decisions.log",
	}
	got := make(map[string]struct{}, len(Seed))
	for _, d := range Seed {
		got[d.Key] = struct{}{}
	}
	for _, k := range expected {
		if _, ok := got[k]; !ok {
			t.Errorf("seed is missing expected key %q", k)
		}
	}
}

// TestSeedDefaults_OSCAL_and_Board_AreOff covers AC-2: integrations
// default off, capability defaults on. AC-12 in the PRD.
func TestSeedDefaults_OSCAL_and_Board_AreOff(t *testing.T) {
	for _, d := range Seed {
		if d.Key == "oscal.export" && d.Enabled {
			t.Errorf("oscal.export must default to disabled (integrations are opt-in)")
		}
		if d.Key == "board.reporting" && d.Enabled {
			t.Errorf("board.reporting must default to disabled (deferred capability)")
		}
	}
}

// TestIsSpineForbidden_PositiveCases asserts every reserved namespace
// trips the gate.
func TestIsSpineForbidden_PositiveCases(t *testing.T) {
	cases := []string{
		"rls",
		"rls.policies",
		"tenancy",
		"tenancy.guc",
		"auth",
		"auth.bearer",
		"schema.registry",
		"schema.registry.kinds",
		"scope.dimensions",
		"scope.cells",
		"scope.applicability",
		"evidence.ledger",
		"evidence.ledger.append",
		"evidence.ingest",
		"framework.crosswalk",
		"framework.crosswalk.strm",
		"framework.requirements",
		"framework.scope",
		"controls.core",
		"controls.spine",
	}
	for _, c := range cases {
		if !IsSpineForbidden(c) {
			t.Errorf("IsSpineForbidden(%q) = false; want true (this key gates a spine surface)", c)
		}
	}
}

// TestIsSpineForbidden_NegativeCases asserts capability flags pass.
func TestIsSpineForbidden_NegativeCases(t *testing.T) {
	cases := []string{
		"risk.enabled",
		"risk.themes",
		"vendor.enabled",
		"policy.enabled",
		"policy.acknowledgments",
		"controls.bundles", // bundles is a capability, not spine
		"exceptions.enabled",
		"audit.workflow",
		"oscal.export",
		"board.reporting",
		"decisions.log",
		"new.capability.flag",
		// Edge: a key that contains a forbidden substring but is not a
		// prefix match. "scope.builder" is not real but illustrates the
		// boundary: "scope.dimensions" is forbidden, "scope.builder" is
		// fine because no spine-forbidden prefix matches it.
		"scope.builder",
	}
	for _, c := range cases {
		if IsSpineForbidden(c) {
			t.Errorf("IsSpineForbidden(%q) = true; want false (this is a capability flag)", c)
		}
	}
}
