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
	// Slice 516 — FULL Security Rule coverage (incl. §164.314 organizational +
	// §164.316 documentation), no longer the slice-481 curated 31-row subset.
	// The exact-count assertion (AC-1) lives in TestLoad_HIPAAFullCoverage below;
	// here we assert the file is at full-coverage scale (a regression to the thin
	// subset would fail this).
	if n := len(cw.Requirements); n < 60 {
		t.Fatalf("HIPAA requirements = %d; want full Security Rule coverage (>=60)", n)
	}
	if len(cw.Mappings) < 60 {
		t.Fatalf("expected >=60 HIPAA mappings (full coverage); got %d", len(cw.Mappings))
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

// AC-1 — full Security Rule coverage spans ALL FIVE Subpart C sections:
// Administrative (§164.308), Physical (§164.310), Technical (§164.312),
// Organizational (§164.314 — new in slice 516), and Policies/Documentation
// (§164.316 — new in slice 516). Every requirement code must fall under one of
// these five sections so the catalog covers the full Security Rule shape (not
// just the three safeguard categories the slice-481 subset carried).
func TestLoad_HIPAASpansAllSecurityRuleSections(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
	}
	var admin, physical, technical, organizational, documentation int
	for _, r := range cw.Requirements {
		switch {
		case strings.HasPrefix(r.Code, "164.308"):
			admin++
		case strings.HasPrefix(r.Code, "164.310"):
			physical++
		case strings.HasPrefix(r.Code, "164.312"):
			technical++
		case strings.HasPrefix(r.Code, "164.314"):
			organizational++
		case strings.HasPrefix(r.Code, "164.316"):
			documentation++
		default:
			t.Fatalf("HIPAA requirement code %q is not under §164.308/310/312/314/316 — unexpected Security Rule section", r.Code)
		}
	}
	if admin == 0 {
		t.Fatal("HIPAA coverage is missing Administrative safeguards (§164.308)")
	}
	if physical == 0 {
		t.Fatal("HIPAA coverage is missing Physical safeguards (§164.310)")
	}
	if technical == 0 {
		t.Fatal("HIPAA coverage is missing Technical safeguards (§164.312)")
	}
	if organizational == 0 {
		t.Fatal("HIPAA coverage is missing Organizational requirements (§164.314) — slice 516 must add them")
	}
	if documentation == 0 {
		t.Fatal("HIPAA coverage is missing Policies/Procedures/Documentation (§164.316) — slice 516 must add them")
	}
}

// Slice 516 AC-1 — FULL HIPAA Security Rule coverage. The shipped crosswalk
// covers all 67 standards + implementation specifications across the five
// Subpart C sections, each with exactly one STRM-typed SCF-anchor edge. This
// asserts the exact full count (a row added or dropped fails loudly so the count
// stays deliberate) and the per-section distribution, retaining all-five-section
// coverage. Mirrors slice 514's TestLoad_CSFFullSubcategoryCoverage.
func TestLoad_HIPAAFullCoverage(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(hipaaPath())
	if err != nil {
		t.Fatalf("Load HIPAA: %v", err)
	}

	// 45 CFR Part 164 Subpart C — full standard + implementation-specification
	// coverage at the grain this catalog ships (see decisions log D2).
	const wantRequirements = 67
	if n := len(cw.Requirements); n != wantRequirements {
		t.Fatalf("HIPAA requirements = %d; want full coverage = %d", n, wantRequirements)
	}
	// One STRM edge per requirement (1:1 — no requirement left unmapped, none
	// mapped twice).
	if n := len(cw.Mappings); n != wantRequirements {
		t.Fatalf("HIPAA mappings = %d; want one edge per requirement = %d", n, wantRequirements)
	}

	// Per-section distribution across the five Subpart C sections.
	perSection := map[string]int{}
	for _, r := range cw.Requirements {
		sec, _, _ := strings.Cut(r.Code, "(")
		perSection[sec]++
	}
	want := map[string]int{
		"164.308": 30, // Administrative safeguards
		"164.310": 12, // Physical safeguards
		"164.312": 12, // Technical safeguards
		"164.314": 8,  // Organizational requirements (new in slice 516)
		"164.316": 5,  // Policies, procedures, and documentation (new in slice 516)
	}
	for sec, n := range want {
		if perSection[sec] != n {
			t.Fatalf("HIPAA section %s has %d requirements; want %d", sec, perSection[sec], n)
		}
	}

	// Every requirement code is unique and every mapping resolves to a declared
	// requirement (guards against a typo'd requirement_code in the data file).
	reqSet := map[string]struct{}{}
	for _, r := range cw.Requirements {
		if _, dup := reqSet[r.Code]; dup {
			t.Fatalf("duplicate HIPAA requirement code %q", r.Code)
		}
		reqSet[r.Code] = struct{}{}
	}
	mapped := map[string]struct{}{}
	for _, m := range cw.Mappings {
		if _, ok := reqSet[m.RequirementCode]; !ok {
			t.Fatalf("mapping references undeclared requirement %q", m.RequirementCode)
		}
		if _, dup := mapped[m.RequirementCode]; dup {
			t.Fatalf("requirement %q mapped more than once", m.RequirementCode)
		}
		mapped[m.RequirementCode] = struct{}{}
	}
	if len(mapped) != len(reqSet) {
		t.Fatalf("mapped requirements = %d; want every one of %d mapped", len(mapped), len(reqSet))
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
