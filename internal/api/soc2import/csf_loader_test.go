package soc2import_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Slice 480 — pure-Go unit coverage (AC-1 / AC-7) for the FOURTH framework
// (NIST CSF 2.0) loaded through the slice-438 framework-agnostic importer.
// These run without a DB (no build tag) so a malformed CSF YAML fails as a
// unit-test failure before integration runs (slice-353 Q-2 convention). No
// new validation branch is exercised by CSF data, so per AC-7 these tests
// assert the EXISTING namespacing + invariant-#7 loader branches with CSF
// code fixtures.

func csfPath() string {
	return filepath.Join("..", "..", "..", "data", "crosswalks", "nist-csf-2.0.yaml")
}

// AC-1 — the shipped CSF 2.0 DRAFT crosswalk parses, is keyed to
// (nist_csf, 2.0), uses the generic requirement_code key, and carries the
// curated high-signal subset. NO CSF-specific loader code is involved — this
// is soc2import.Load, the same entry point SOC 2 / ISO / PCI use (P0-480-1).
func TestLoad_ShippedCSFCrosswalkParses(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(csfPath())
	if err != nil {
		t.Fatalf("Load(%s): %v", csfPath(), err)
	}
	if cw.FrameworkSlug != "nist_csf" {
		t.Fatalf("framework_slug = %q; want nist_csf", cw.FrameworkSlug)
	}
	if cw.FrameworkVersion != "2.0" {
		t.Fatalf("framework_version = %q; want 2.0", cw.FrameworkVersion)
	}
	// Curated subset spanning all six Functions, NOT full CSF Subcategory
	// coverage (scope discipline — P0-480-6 deferral).
	if n := len(cw.Requirements); n < 30 || n > 40 {
		t.Fatalf("CSF requirements = %d; want curated subset in [30,40]", n)
	}
	if len(cw.Mappings) < 30 {
		t.Fatalf("expected >=30 CSF mappings; got %d", len(cw.Mappings))
	}
	if cw.SourceAttribution != "community_draft" {
		t.Fatalf("crosswalk-level source_attribution = %q; want community_draft", cw.SourceAttribution)
	}
	// Every mapping's RequirementCode must be populated from the generic
	// requirement_code key, and CSF codes are dotted FN.CAT-NN (e.g. "PR.AA-01").
	for i, m := range cw.Mappings {
		if m.RequirementCode == "" {
			t.Fatalf("mapping[%d] has empty RequirementCode — requirement_code key not parsed", i)
		}
	}
}

// AC-1 — the CSF subset spans ALL SIX CSF 2.0 Functions (GOVERN, IDENTIFY,
// PROTECT, DETECT, RESPOND, RECOVER). This is the scope-discipline guarantee
// the slice narrative binds: every Function must be represented so the GOVERN
// no-analog proof and the cross-Function coverage both hold.
func TestLoad_CSFSpansAllSixFunctions(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(csfPath())
	if err != nil {
		t.Fatalf("Load CSF: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range cw.Requirements {
		fn, _, ok := strings.Cut(r.Code, ".")
		if !ok {
			t.Fatalf("CSF requirement code %q is not in FN.CAT-NN form", r.Code)
		}
		seen[fn] = true
	}
	for _, fn := range []string{"GV", "ID", "PR", "DE", "RS", "RC"} {
		if !seen[fn] {
			t.Fatalf("CSF subset is missing Function %q — all six Functions must be represented", fn)
		}
	}
}

// AC-5 (unit-level precursor) — the CSF GOVERN function has no SOC 2 analog;
// at least one GV.* Subcategory must map to an SCF governance-family anchor
// (GOV-*). The DB-level proof is in the integration suite; this asserts the
// data carries the no-analog mapping at all.
func TestLoad_CSFGovernMapsToGovernanceFamily(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(csfPath())
	if err != nil {
		t.Fatalf("Load CSF: %v", err)
	}
	var governToGOV int
	for _, m := range cw.Mappings {
		if strings.HasPrefix(m.RequirementCode, "GV.") && strings.HasPrefix(m.SCFAnchor, "GOV-") {
			governToGOV++
		}
	}
	if governToGOV == 0 {
		t.Fatal("no CSF GOVERN Subcategory maps to an SCF GOV-* anchor — AC-5 no-analog proof has no data backing")
	}
}

// AC-7 / P0-480-2 — every CSF mapping targets an SCF anchor, never another
// requirement (invariant #7). Asserts the existing loader branch with CSF
// fixtures: no requirement_code appears as an scf_anchor target.
func TestLoad_CSFEveryMappingTargetsAnSCFAnchor(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(csfPath())
	if err != nil {
		t.Fatalf("Load CSF: %v", err)
	}
	reqCodes := map[string]struct{}{}
	for _, r := range cw.Requirements {
		reqCodes[r.Code] = struct{}{}
	}
	for i, m := range cw.Mappings {
		if m.SCFAnchor == "" {
			t.Fatalf("mapping[%d] (%s) has empty scf_anchor — invariant #7 requires a requirement -> SCF anchor edge", i, m.RequirementCode)
		}
		if _, isReq := reqCodes[m.SCFAnchor]; isReq {
			t.Fatalf("mapping[%d] targets CSF requirement %q as its anchor — requirement -> requirement edge (invariant #7 violation)", i, m.SCFAnchor)
		}
		if m.RelationshipType == "" {
			t.Fatalf("mapping[%d] (%s -> %s) has empty relationship_type", i, m.RequirementCode, m.SCFAnchor)
		}
	}
}

// AC-7 — cross-framework code namespacing across FOUR frameworks: a CSF
// "PR.AA-01", a PCI "8.3.1", an ISO "A.5.15", and a SOC 2 "CC6.1" are distinct
// strings, so the four separately-loaded crosswalks never collide on
// requirement code. The DB-level proof (distinct rows under distinct
// framework_version_ids) is in the integration suite (AC-6).
func TestLoad_CSFCodesDistinctFromOtherFrameworks(t *testing.T) {
	t.Parallel()
	csf, err := soc2import.Load(csfPath())
	if err != nil {
		t.Fatalf("Load CSF: %v", err)
	}
	iso, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "iso27001-2022.yaml"))
	if err != nil {
		t.Fatalf("Load ISO: %v", err)
	}
	pci, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "pci-dss-4.0.yaml"))
	if err != nil {
		t.Fatalf("Load PCI: %v", err)
	}
	soc2, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
	if err != nil {
		t.Fatalf("Load SOC 2: %v", err)
	}
	for _, other := range []*soc2import.Crosswalk{iso, pci, soc2} {
		if csf.FrameworkSlug == other.FrameworkSlug {
			t.Fatalf("CSF framework slug %q collides with another framework", csf.FrameworkSlug)
		}
	}
	owned := map[string]string{} // code -> framework that owns it
	for _, r := range iso.Requirements {
		owned[r.Code] = "iso27001"
	}
	for _, r := range pci.Requirements {
		owned[r.Code] = "pcidss"
	}
	for _, r := range soc2.Requirements {
		owned[r.Code] = "soc2"
	}
	for _, r := range csf.Requirements {
		if owner, clash := owned[r.Code]; clash {
			t.Fatalf("CSF requirement code %q also appears in %s — four-framework namespacing assumption broken", r.Code, owner)
		}
	}
}
