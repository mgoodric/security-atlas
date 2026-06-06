package frameworkscope_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// Slice 447 — the FrameworkScope CDE-intersection proof for invariant #5
// (canvas §5.5):
//
//	effective_scope(control, framework) =
//	    control.applicability_expr ∩ framework_scope.predicate
//
// PCI DSS is the framework where invariant #5 is most load-bearing: the
// Cardholder Data Environment (CDE) is a deliberately narrow scope, distinct
// from a broad SOC 2 system boundary. This test takes ONE control that is
// applicable org-wide and shows the SAME control's effective scope DIFFERS by
// framework — narrowed to the CDE under the PCI framework_scope predicate,
// while staying org-wide under SOC 2's (true) predicate. That per-framework
// divergence for one control is the invariant-#5 demonstration (AC-4 / AC-5).
//
// This uses the existing slice-018 FrameworkScope machinery
// (frameworkscope.Canonicalize + frameworkscope.EffectiveScope) with NO new
// scope code (P0-447-4 — the CDE predicate goes through the same predicate
// path; it does not bypass the approval lifecycle).

// orgWideUniverse is a representative tenant cell universe: prod CDE systems
// that handle cardholder data, plus non-CDE systems (a marketing site, a dev
// environment) that are inside the SOC 2 system boundary but OUTSIDE the PCI
// CDE.
func orgWideUniverse() []scope.Cell {
	return []scope.Cell{
		{ID: uuid.New(), Label: "payments-prod-cde", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "restricted", "product_line": "payments"}},
		{ID: uuid.New(), Label: "payments-prod-cde-db", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "restricted", "product_line": "payments"}},
		{ID: uuid.New(), Label: "marketing-prod", Dimensions: map[string]string{
			"environment": "prod", "data_classification": "public", "product_line": "marketing"}},
		{ID: uuid.New(), Label: "internal-dev", Dimensions: map[string]string{
			"environment": "dev", "data_classification": "internal", "product_line": "platform"}},
	}
}

// cdePredicate is the PCI framework_scope predicate: the CDE is the set of
// cells that store, process, or transmit cardholder data — modeled here as
// prod systems classified `restricted`. Authored as a slice-018 predicate
// (the same JSON-AST the approval lifecycle validates).
func cdePredicate(t *testing.T) []byte {
	t.Helper()
	canon, _, err := frameworkscope.Canonicalize([]byte(
		`{"op":"and","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"eq","dim":"data_classification","value":"restricted"}]}`))
	if err != nil {
		t.Fatalf("canonicalize CDE predicate: %v", err)
	}
	return canon
}

// soc2Predicate is the SOC 2 framework_scope predicate: the SOC 2 system
// boundary is broad — the platform-default `true` (no narrowing). This is the
// slice-018 default-seed behaviour.
func soc2Predicate(t *testing.T) []byte {
	t.Helper()
	canon, _, err := frameworkscope.Canonicalize([]byte(`{"op":"true"}`))
	if err != nil {
		t.Fatalf("canonicalize SOC 2 predicate: %v", err)
	}
	return canon
}

// AC-4 — a worked example: an org-wide control's effective scope is narrowed
// to the CDE under the PCI framework_scope predicate. The control here is the
// access-control / authentication control that maps to SCF IAC-01 — the same
// anchor shared across SOC 2 CC6.1, ISO A.5.15, and PCI 8.2.1. The control is
// applicable org-wide (applicability_expr matches every cell), but under PCI
// its effective scope is only the two CDE cells.
func TestCDEIntersection_NarrowsOrgWideControlToCDE(t *testing.T) {
	universe := orgWideUniverse()
	// The control applies org-wide: applicability_expr = true => all cells.
	applicability, err := scope.Evaluate([]byte(`{"op":"true"}`), universe)
	if err != nil {
		t.Fatalf("evaluate applicability: %v", err)
	}
	if len(applicability) != len(universe) {
		t.Fatalf("org-wide control should apply to all %d cells; got %d", len(universe), len(applicability))
	}

	// effective_scope under PCI = applicability ∩ CDE predicate.
	pciEffective, err := frameworkscope.EffectiveScope(context.Background(), applicability, cdePredicate(t))
	if err != nil {
		t.Fatalf("PCI EffectiveScope: %v", err)
	}
	if len(pciEffective) != 2 {
		t.Fatalf("PCI effective scope should narrow the org-wide control to the 2 CDE cells; got %d", len(pciEffective))
	}
	for _, c := range pciEffective {
		if c.Dimensions["environment"] != "prod" || c.Dimensions["data_classification"] != "restricted" {
			t.Fatalf("cell %q leaked into the PCI effective scope but is outside the CDE: %v", c.Label, c.Dimensions)
		}
	}
}

// AC-5 — THE LOAD-BEARING invariant-#5 assertion: the SAME org-wide control
// has a DIFFERENT effective scope per framework. Under PCI it is narrowed to
// the CDE (2 cells); under SOC 2 it stays org-wide (all 4 cells). The
// intersection differs per framework — invariant #5 — for the framework
// (PCI) where the contrast matters most.
func TestCDEIntersection_EffectiveScopeDiffersPerFramework_Invariant5(t *testing.T) {
	universe := orgWideUniverse()
	applicability, err := scope.Evaluate([]byte(`{"op":"true"}`), universe)
	if err != nil {
		t.Fatalf("evaluate applicability: %v", err)
	}

	pciEffective, err := frameworkscope.EffectiveScope(context.Background(), applicability, cdePredicate(t))
	if err != nil {
		t.Fatalf("PCI EffectiveScope: %v", err)
	}
	soc2Effective, err := frameworkscope.EffectiveScope(context.Background(), applicability, soc2Predicate(t))
	if err != nil {
		t.Fatalf("SOC 2 EffectiveScope: %v", err)
	}

	// The contrast is the proof: same control, two frameworks, two effective
	// scopes. PCI narrows to the CDE; SOC 2 keeps the full system boundary.
	if len(pciEffective) >= len(soc2Effective) {
		t.Fatalf("invariant #5 unproven: PCI effective scope (%d) should be STRICTLY narrower than SOC 2 (%d) for the same org-wide control",
			len(pciEffective), len(soc2Effective))
	}
	if len(soc2Effective) != len(universe) {
		t.Fatalf("SOC 2 effective scope should equal the full org-wide universe (%d); got %d", len(universe), len(soc2Effective))
	}
	if len(pciEffective) != 2 {
		t.Fatalf("PCI effective scope should be the 2 CDE cells; got %d", len(pciEffective))
	}

	// Concretely: the marketing-prod and internal-dev cells are in SOC 2's
	// effective scope but NOT in PCI's CDE effective scope.
	inScope := func(cells []scope.Cell, label string) bool {
		for _, c := range cells {
			if c.Label == label {
				return true
			}
		}
		return false
	}
	if !inScope(soc2Effective, "marketing-prod") {
		t.Fatal("marketing-prod should be in the SOC 2 effective scope")
	}
	if inScope(pciEffective, "marketing-prod") {
		t.Fatal("marketing-prod must NOT be in the PCI CDE effective scope — it does not store cardholder data")
	}
	t.Logf("invariant #5 proven: same control resolves to SOC 2=%d cells (org-wide) vs PCI=%d cells (CDE-only)",
		len(soc2Effective), len(pciEffective))
}
