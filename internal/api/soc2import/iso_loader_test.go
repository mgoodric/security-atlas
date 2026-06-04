package soc2import_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Slice 438 — pure-Go unit coverage (AC-9) for the framework-agnostic
// generalization. These run without a DB (no build tag) so the loader's
// new branches get fast feedback per the slice-353 Q-2 convention.

// AC-4 — the shipped ISO 27001:2022 DRAFT crosswalk parses, is keyed to
// (iso27001, 2022), and carries the curated high-signal subset (target
// 30-40 Annex A controls). CI runs this without Postgres so a malformed
// ISO YAML fails as a unit-test failure before integration runs.
func TestLoad_ShippedISOCrosswalkParses(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "data", "crosswalks", "iso27001-2022.yaml")
	cw, err := soc2import.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	if cw.FrameworkSlug != "iso27001" {
		t.Fatalf("framework_slug = %q; want iso27001", cw.FrameworkSlug)
	}
	if cw.FrameworkVersion != "2022" {
		t.Fatalf("framework_version = %q; want 2022", cw.FrameworkVersion)
	}
	// Curated subset, NOT full 93-Annex-A coverage (scope discipline).
	if n := len(cw.Requirements); n < 30 || n > 45 {
		t.Fatalf("ISO requirements = %d; want curated subset in [30,45]", n)
	}
	if len(cw.Mappings) < 30 {
		t.Fatalf("expected >=30 ISO mappings; got %d", len(cw.Mappings))
	}
	if cw.SourceAttribution != "community_draft" {
		t.Fatalf("crosswalk-level source_attribution = %q; want community_draft", cw.SourceAttribution)
	}
	// Every mapping's RequirementCode must be populated from the generic
	// requirement_code key (the ISO file uses requirement_code, not the
	// legacy tsc_code).
	for i, m := range cw.Mappings {
		if m.RequirementCode == "" {
			t.Fatalf("mapping[%d] has empty RequirementCode — requirement_code key not parsed", i)
		}
		if !strings.HasPrefix(m.RequirementCode, "A.") {
			t.Fatalf("mapping[%d] RequirementCode %q is not an ISO Annex A code", i, m.RequirementCode)
		}
	}
}

// AC-1 backward-compat — the generic loader still reads the legacy
// `tsc_code:` key so the slice-007 SOC 2 crosswalk imports unmodified.
func TestLoad_LegacyTSCCodeKeyStillParses(t *testing.T) {
	t.Parallel()
	tmp := writeTemp(t, `
schema_version: "1.0"
framework_slug: "soc2"
framework_name: "SOC 2"
framework_issuer: "AICPA"
framework_version: "2017"
release_date: "2017-04-01"
source_attribution: "community_draft"
requirements:
  - code: CC6.1
    title: Logical access controls
    body: body
mappings:
  - tsc_code: CC6.1
    scf_anchor: IAC-01
    relationship_type: equal
    strength: 0.9
    rationale: "legacy key"
`)
	cw, err := soc2import.Load(tmp)
	if err != nil {
		t.Fatalf("Load with legacy tsc_code: %v", err)
	}
	if got := cw.Mappings[0].RequirementCode; got != "CC6.1" {
		t.Fatalf("legacy tsc_code did not normalise into RequirementCode; got %q", got)
	}
}

// AC-1 — the generic `requirement_code:` key is the preferred form for new
// crosswalks and parses into RequirementCode.
func TestLoad_GenericRequirementCodeKeyParses(t *testing.T) {
	t.Parallel()
	tmp := writeTemp(t, `
schema_version: "1.0"
framework_slug: "iso27001"
framework_name: "ISO/IEC 27001:2022"
framework_issuer: "ISO/IEC"
framework_version: "2022"
release_date: "2022-10-25"
source_attribution: "community_draft"
requirements:
  - code: A.5.15
    title: Access control
    body: body
mappings:
  - requirement_code: A.5.15
    scf_anchor: IAC-01
    relationship_type: equal
    strength: 0.9
    rationale: "generic key"
`)
	cw, err := soc2import.Load(tmp)
	if err != nil {
		t.Fatalf("Load with requirement_code: %v", err)
	}
	if got := cw.Mappings[0].RequirementCode; got != "A.5.15" {
		t.Fatalf("requirement_code did not parse into RequirementCode; got %q", got)
	}
}

// AC-9 — a mapping that sets neither requirement_code nor tsc_code is
// rejected with a clear error rather than silently dropping the edge.
func TestLoad_RejectsMappingWithNoRequirementCode(t *testing.T) {
	t.Parallel()
	tmp := writeTemp(t, `
schema_version: "1.0"
framework_slug: "iso27001"
framework_name: "ISO/IEC 27001:2022"
framework_issuer: "ISO/IEC"
framework_version: "2022"
release_date: "2022-10-25"
source_attribution: "community_draft"
requirements:
  - code: A.5.15
    title: Access control
    body: body
mappings:
  - scf_anchor: IAC-01
    relationship_type: equal
    strength: 0.9
    rationale: "no requirement code at all"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error when neither requirement_code nor tsc_code is set")
	}
	if !strings.Contains(err.Error(), "requirement_code") {
		t.Fatalf("error should mention requirement_code; got: %v", err)
	}
}

// AC-9 — a mapping referencing a requirement code not declared in the file
// is rejected (the generic-key path of the slice-007 unknown-code guard).
func TestLoad_RejectsMappingWithUnknownRequirementCode(t *testing.T) {
	t.Parallel()
	tmp := writeTemp(t, `
schema_version: "1.0"
framework_slug: "iso27001"
framework_name: "ISO/IEC 27001:2022"
framework_issuer: "ISO/IEC"
framework_version: "2022"
release_date: "2022-10-25"
source_attribution: "community_draft"
requirements:
  - code: A.5.15
    title: Access control
    body: body
mappings:
  - requirement_code: A.9.99
    scf_anchor: IAC-01
    relationship_type: equal
    strength: 0.9
    rationale: "typo'd code"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error for unknown requirement_code")
	}
	if !strings.Contains(err.Error(), "A.9.99") {
		t.Fatalf("error should name the offending code; got: %v", err)
	}
}

// AC-3 — cross-framework code namespacing is structural: an ISO `A.5.1` and
// a SOC 2 `CC5.1` are distinct strings, so two separately-loaded crosswalks
// never collide on requirement code. This unit test documents the invariant
// at the loader surface; the DB-level proof (distinct rows under distinct
// framework_version_ids) is in the integration suite (AC-8).
func TestLoad_CrossFrameworkCodesAreDistinct(t *testing.T) {
	t.Parallel()
	iso, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "iso27001-2022.yaml"))
	if err != nil {
		t.Fatalf("Load ISO: %v", err)
	}
	soc2, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
	if err != nil {
		t.Fatalf("Load SOC 2: %v", err)
	}
	if iso.FrameworkSlug == soc2.FrameworkSlug {
		t.Fatalf("frameworks must differ; both = %q", iso.FrameworkSlug)
	}
	isoCodes := map[string]struct{}{}
	for _, r := range iso.Requirements {
		isoCodes[r.Code] = struct{}{}
	}
	for _, r := range soc2.Requirements {
		if _, clash := isoCodes[r.Code]; clash {
			t.Fatalf("requirement code %q appears in BOTH frameworks — namespacing assumption broken", r.Code)
		}
	}
}
