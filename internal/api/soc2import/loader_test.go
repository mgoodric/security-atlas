package soc2import_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Smoke-test the on-disk DRAFT file. The agent ships this with the slice;
// CI runs this without a database so a malformed YAML lands as a unit-test
// failure long before integration tests run.
func TestLoad_ShippedDraftCrosswalkParses(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml")
	cw, err := soc2import.Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	if cw.FrameworkSlug != "soc2" {
		t.Fatalf("framework_slug = %q; want soc2", cw.FrameworkSlug)
	}
	if cw.FrameworkVersion != "2017" {
		t.Fatalf("framework_version = %q; want 2017", cw.FrameworkVersion)
	}
	if len(cw.Requirements) < 5 {
		t.Fatalf("expected >=5 SOC 2 TSC requirements; got %d", len(cw.Requirements))
	}
	if len(cw.Mappings) < 30 {
		t.Fatalf("expected >=30 drafted mappings; got %d", len(cw.Mappings))
	}
	// Every drafted row defaults to community_draft attribution — the HITL
	// signal an auditor reading the DB sees.
	if cw.SourceAttribution != "community_draft" {
		t.Fatalf("crosswalk-level source_attribution = %q; want community_draft", cw.SourceAttribution)
	}
}

// AC-2 + ISC-12: relationship_type and strength are mandatory on every
// mapping; the loader rejects rows that omit either rather than silently
// defaulting. Anti-criterion guard.
func TestLoad_RejectsMissingRelationshipType(t *testing.T) {
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
    body: short body
mappings:
  - tsc_code: CC6.1
    scf_anchor: IAC-01
    strength: 0.8
    rationale: "ambiguous"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error when relationship_type is missing")
	}
	if !strings.Contains(err.Error(), "relationship_type") {
		t.Fatalf("error should mention relationship_type; got: %v", err)
	}
}

// ISC-13: strength out of [0.0, 1.0] is rejected by the loader BEFORE the
// DB CHECK constraint fires so the error message names the offending row.
func TestLoad_RejectsStrengthOutOfRange(t *testing.T) {
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
    strength: 1.4
    rationale: "out of range"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error when strength > 1.0")
	}
	if !strings.Contains(err.Error(), "strength") {
		t.Fatalf("error should mention strength; got: %v", err)
	}
}

// ISC-4: only the canvas-defined STRM literals are accepted. The legacy
// slice-005 anchorseed used "intersects" (no _with) — that string must be
// rejected so we don't accidentally write it into the DB.
func TestLoad_RejectsLegacyRelationshipTypeSpelling(t *testing.T) {
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
    relationship_type: intersects   # missing _with
    strength: 0.6
    rationale: "wrong spelling"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error for legacy 'intersects' spelling")
	}
}

// ISC-5: source_attribution must be one of the three canonical values.
// "community" without "_draft" is rejected so the HITL signal stays sharp.
func TestLoad_RejectsAmbiguousSourceAttribution(t *testing.T) {
	t.Parallel()
	tmp := writeTemp(t, `
schema_version: "1.0"
framework_slug: "soc2"
framework_name: "SOC 2"
framework_issuer: "AICPA"
framework_version: "2017"
release_date: "2017-04-01"
source_attribution: "community"
requirements:
  - code: CC6.1
    title: Logical access controls
    body: body
mappings:
  - tsc_code: CC6.1
    scf_anchor: IAC-01
    relationship_type: equal
    strength: 1.0
    rationale: "ok"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error for ambiguous 'community' source_attribution")
	}
}

// ISC-12: a mapping that references a tsc_code not declared in the
// requirements list is rejected. Catches typos in the crosswalk before
// they reach the DB.
func TestLoad_RejectsMappingWithUnknownTSCCode(t *testing.T) {
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
  - tsc_code: CC9.99
    scf_anchor: IAC-01
    relationship_type: equal
    strength: 1.0
    rationale: "ok"
`)
	_, err := soc2import.Load(tmp)
	if err == nil {
		t.Fatal("expected error for unknown tsc_code")
	}
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "crosswalk-*.yaml")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(strings.TrimSpace(body) + "\n"); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	_ = f.Close()
	return f.Name()
}
