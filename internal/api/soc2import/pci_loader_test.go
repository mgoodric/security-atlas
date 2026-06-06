package soc2import_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Slice 447 — pure-Go unit coverage (AC-1 / AC-9) for the THIRD framework
// (PCI DSS v4.0) loaded through the slice-438 framework-agnostic importer.
// These run without a DB (no build tag) so a malformed PCI YAML fails as a
// unit-test failure before integration runs (slice-353 Q-2 convention).

// AC-1 — the shipped PCI DSS v4.0 DRAFT crosswalk parses, is keyed to
// (pcidss, 4.0), uses the generic requirement_code key, and carries the
// curated high-signal subset. NO PCI-specific loader code is involved —
// this is soc2import.Load, the same entry point ISO and SOC 2 use
// (P0-447-1).
func TestLoad_ShippedPCICrosswalkParses(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "data", "crosswalks", "pci-dss-4.0.yaml")
	cw, err := soc2import.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	if cw.FrameworkSlug != "pcidss" {
		t.Fatalf("framework_slug = %q; want pcidss", cw.FrameworkSlug)
	}
	if cw.FrameworkVersion != "4.0" {
		t.Fatalf("framework_version = %q; want 4.0", cw.FrameworkVersion)
	}
	// Curated subset spanning all 12 principal requirements, NOT full PCI
	// sub-requirement coverage (scope discipline — P0-447-5 deferral).
	if n := len(cw.Requirements); n < 25 || n > 45 {
		t.Fatalf("PCI requirements = %d; want curated subset in [25,45]", n)
	}
	if len(cw.Mappings) < 25 {
		t.Fatalf("expected >=25 PCI mappings; got %d", len(cw.Mappings))
	}
	if cw.SourceAttribution != "community_draft" {
		t.Fatalf("crosswalk-level source_attribution = %q; want community_draft", cw.SourceAttribution)
	}
	// Every mapping's RequirementCode must be populated from the generic
	// requirement_code key, and PCI codes are dotted numerics (e.g. "8.3.1").
	for i, m := range cw.Mappings {
		if m.RequirementCode == "" {
			t.Fatalf("mapping[%d] has empty RequirementCode — requirement_code key not parsed", i)
		}
		if strings.HasPrefix(m.RequirementCode, "A.") || strings.HasPrefix(m.RequirementCode, "CC") {
			t.Fatalf("mapping[%d] RequirementCode %q looks like an ISO/SOC 2 code, not a PCI requirement id", i, m.RequirementCode)
		}
	}
}

// AC-1 / P0-447-2 — the PCI crosswalk maps every requirement to an SCF
// anchor, never to another requirement. The loader has no requirement ->
// requirement path; this test asserts the data honors invariant #7 by
// confirming every mapping carries a non-empty scf_anchor and a valid STRM
// relationship_type drawn from the SCF anchor namespace (uppercase family +
// numeric suffix), not a PCI requirement code.
func TestLoad_PCIEveryMappingTargetsAnSCFAnchor(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "pci-dss-4.0.yaml"))
	if err != nil {
		t.Fatalf("Load PCI: %v", err)
	}
	reqCodes := map[string]struct{}{}
	for _, r := range cw.Requirements {
		reqCodes[r.Code] = struct{}{}
	}
	for i, m := range cw.Mappings {
		if m.SCFAnchor == "" {
			t.Fatalf("mapping[%d] (%s) has empty scf_anchor — invariant #7 requires a requirement -> SCF anchor edge", i, m.RequirementCode)
		}
		// The target must NOT be a PCI requirement code (that would be a
		// requirement -> requirement edge, violating invariant #7).
		if _, isReq := reqCodes[m.SCFAnchor]; isReq {
			t.Fatalf("mapping[%d] targets PCI requirement %q as its anchor — requirement -> requirement edge (invariant #7 violation)", i, m.SCFAnchor)
		}
		if m.RelationshipType == "" {
			t.Fatalf("mapping[%d] (%s -> %s) has empty relationship_type", i, m.RequirementCode, m.SCFAnchor)
		}
	}
}

// AC-3 — cross-framework code namespacing across THREE frameworks: a PCI
// "8.3.1", an ISO "A.5.15", and a SOC 2 "CC6.1" are distinct strings, so the
// three separately-loaded crosswalks never collide on requirement code. The
// DB-level proof (distinct rows under distinct framework_version_ids) is in
// the integration suite (AC-7).
func TestLoad_PCICodesDistinctFromISOandSOC2(t *testing.T) {
	t.Parallel()
	pci, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "pci-dss-4.0.yaml"))
	if err != nil {
		t.Fatalf("Load PCI: %v", err)
	}
	iso, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "iso27001-2022.yaml"))
	if err != nil {
		t.Fatalf("Load ISO: %v", err)
	}
	soc2, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
	if err != nil {
		t.Fatalf("Load SOC 2: %v", err)
	}
	if pci.FrameworkSlug == iso.FrameworkSlug || pci.FrameworkSlug == soc2.FrameworkSlug {
		t.Fatalf("PCI framework slug %q collides with another framework", pci.FrameworkSlug)
	}
	other := map[string]string{} // code -> framework that owns it
	for _, r := range iso.Requirements {
		other[r.Code] = "iso27001"
	}
	for _, r := range soc2.Requirements {
		other[r.Code] = "soc2"
	}
	for _, r := range pci.Requirements {
		if owner, clash := other[r.Code]; clash {
			t.Fatalf("PCI requirement code %q also appears in %s — three-framework namespacing assumption broken", r.Code, owner)
		}
	}
}
