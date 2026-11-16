package soc2import_test

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// #2026: a YAML crosswalk's points_of_focus is held to the shape the
// workbook roll-up produces: a point-of-focus code of the requirement it
// sits on, listed once, with at least one non-blank SCF anchor.
func TestLoad_ValidatesYAMLPointsOfFocus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pof     string
		wantErr string
	}{
		{"not a point-of-focus code", `
      - code: CC1.1
        scf_anchors: [GOV-01]`, "is not a point-of-focus code"},
		{"another criterion's point of focus", `
      - code: CC2.1-POF1
        scf_anchors: [GOV-01]`, "belongs to criterion CC2.1"},
		{"listed twice", `
      - code: CC1.1-POF1
        scf_anchors: [GOV-01]
      - code: CC1.1-POF1
        scf_anchors: [IAC-01]`, "twice"},
		{"no anchors", `
      - code: CC1.1-POF1
        scf_anchors: []`, "has no scf_anchors"},
		{"anchors key missing", `
      - code: CC1.1-POF1`, "has no scf_anchors"},
		{"blank anchor", `
      - code: CC1.1-POF1
        scf_anchors: ["GOV-01", " "]`, "scf_anchors[1] is blank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := soc2import.Load(writeTemp(t, `
schema_version: "1.0"
framework_slug: soc2
framework_version: "2017"
source_attribution: community_draft
requirements:
  - code: CC1.1
    title: Our wording for criterion one
    points_of_focus:`+tc.pof+`
mappings:
  - requirement_code: CC1.1
    scf_anchor: GOV-01
    relationship_type: intersects_with
    strength: 0.5
    rationale: test
`))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load err = %v; want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
