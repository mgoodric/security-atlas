package soc2import_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Slice 481 — pure-Go unit coverage (AC-1 / AC-7) for the FIFTH framework
// (HIPAA Security Rule) loaded through the slice-438 framework-agnostic
// importer. These run without a DB (no build tag) so a malformed HIPAA YAML
// fails as a unit-test failure before integration runs (slice-353 Q-2
// convention). No new validation branch is exercised by HIPAA data — per AC-7
// these tests assert the EXISTING namespacing + invariant-#7 loader branches
// with HIPAA code fixtures. This slice is CATALOG-ONLY: it ships requirement
// nodes + STRM edges, NOT the covered-entity workflow (P0-481-1).

func hipaaPath() string {
	return filepath.Join("..", "..", "..", "data", "crosswalks", "hipaa-security-rule.yaml")
}

// AC-1 — the shipped HIPAA Security Rule DRAFT crosswalk parses, is keyed to
// (hipaa_security_rule, 2013-01-25), uses the generic requirement_code key, and
// carries the curated high-signal subset. NO HIPAA-specific loader code is
// involved — this is soc2import.Load, the same entry point SOC 2 / ISO / PCI /
// CSF use (P0-481-2).
func TestLoad_ShippedHIPAACrosswalkParses(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load(%s): %v", hipaaPath(), err)
	}
	if cw.FrameworkSlug != "hipaa_security_rule" {
		t.Fatalf("framework_slug = %q; want hipaa_security_rule", cw.FrameworkSlug)
	}
	if cw.FrameworkVersion != "2013-01-25" {
		t.Fatalf("framework_version = %q; want 2013-01-25", cw.FrameworkVersion)
	}
	// Curated subset spanning all three safeguard categories, NOT full Security
	// Rule coverage (scope discipline — P0-481-1 catalog-only framing).
	if n := len(cw.Requirements); n < 25 || n > 35 {
		t.Fatalf("HIPAA requirements = %d; want curated subset in [25,35]", n)
	}
	if len(cw.Mappings) < 25 {
		t.Fatalf("expected >=25 HIPAA mappings; got %d", len(cw.Mappings))
	}
	if cw.SourceAttribution != "community_draft" {
		t.Fatalf("crosswalk-level source_attribution = %q; want community_draft", cw.SourceAttribution)
	}
	// Every mapping's RequirementCode must be populated from the generic
	// requirement_code key, and HIPAA codes are CFR section identifiers
	// (e.g. "164.312(a)(1)").
	for i, m := range cw.Mappings {
		if m.RequirementCode == "" {
			t.Fatalf("mapping[%d] has empty RequirementCode — requirement_code key not parsed", i)
		}
	}
}

// AC-1 — the HIPAA subset spans ALL THREE safeguard categories: Administrative
// (§164.308), Physical (§164.310), and Technical (§164.312). This is the
// scope-discipline guarantee the slice narrative binds: every safeguard
// category must be represented so the catalog covers the full Security Rule
// shape (not just the technical safeguards an engineer reaches for first).
func TestLoad_HIPAASpansAllThreeSafeguardCategories(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
	}
	var admin, physical, technical int
	for _, r := range cw.Requirements {
		switch {
		case strings.HasPrefix(r.Code, "164.308"):
			admin++
		case strings.HasPrefix(r.Code, "164.310"):
			physical++
		case strings.HasPrefix(r.Code, "164.312"):
			technical++
		default:
			t.Fatalf("HIPAA requirement code %q is not under §164.308/310/312 — unexpected safeguard section", r.Code)
		}
	}
	if admin == 0 {
		t.Fatal("HIPAA subset is missing Administrative safeguards (§164.308)")
	}
	if physical == 0 {
		t.Fatal("HIPAA subset is missing Physical safeguards (§164.310)")
	}
	if technical == 0 {
		t.Fatal("HIPAA subset is missing Technical safeguards (§164.312)")
	}
}

// AC-4 (unit-level precursor) — the HIPAA Technical Access Control standard
// (§164.312(a)(1)) maps to IAC-01, the five-framework shared anchor. The DB
// proof is in the integration suite; this asserts the data carries the shared
// anchor at all.
func TestLoad_HIPAAAccessControlMapsToSharedAnchor(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
	}
	var found bool
	for _, m := range cw.Mappings {
		if m.RequirementCode == "164.312(a)(1)" {
			found = true
			if m.SCFAnchor != "IAC-01" {
				t.Fatalf("164.312(a)(1) should map to IAC-01 (five-framework shared anchor); got %q", m.SCFAnchor)
			}
		}
	}
	if !found {
		t.Fatal("no mapping for HIPAA 164.312(a)(1) — AC-4 shared-anchor proof has no data backing")
	}
}

// AC-7 / P0-481-3 — every HIPAA mapping targets an SCF anchor, never another
// requirement (invariant #7). Asserts the existing loader branch with HIPAA
// fixtures: no requirement_code appears as an scf_anchor target.
func TestLoad_HIPAAEveryMappingTargetsAnSCFAnchor(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
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
			t.Fatalf("mapping[%d] targets HIPAA requirement %q as its anchor — requirement -> requirement edge (invariant #7 violation)", i, m.SCFAnchor)
		}
		if m.RelationshipType == "" {
			t.Fatalf("mapping[%d] (%s -> %s) has empty relationship_type", i, m.RequirementCode, m.SCFAnchor)
		}
	}
}

// AC-7 — cross-framework code namespacing across FIVE frameworks: a HIPAA
// "164.312(a)(1)", a CSF "PR.AA-01", a PCI "8.3.1", an ISO "A.5.15", and a
// SOC 2 "CC6.1" are distinct strings, so the five separately-loaded crosswalks
// never collide on requirement code. The DB-level proof (distinct rows under
// distinct framework_version_ids) is in the integration suite (AC-6).
func TestLoad_HIPAACodesDistinctFromOtherFrameworks(t *testing.T) {
	t.Parallel()
	hipaa, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
	}
	csf, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "nist-csf-2.0.yaml"))
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
	for _, other := range []*soc2import.Crosswalk{csf, iso, pci, soc2} {
		if hipaa.FrameworkSlug == other.FrameworkSlug {
			t.Fatalf("HIPAA framework slug %q collides with another framework", hipaa.FrameworkSlug)
		}
	}
	owned := map[string]string{} // code -> framework that owns it
	for _, r := range csf.Requirements {
		owned[r.Code] = "nist_csf"
	}
	for _, r := range iso.Requirements {
		owned[r.Code] = "iso27001"
	}
	for _, r := range pci.Requirements {
		owned[r.Code] = "pcidss"
	}
	for _, r := range soc2.Requirements {
		owned[r.Code] = "soc2"
	}
	for _, r := range hipaa.Requirements {
		if owner, clash := owned[r.Code]; clash {
			t.Fatalf("HIPAA requirement code %q also appears in %s — five-framework namespacing assumption broken", r.Code, owner)
		}
	}
}
