package demoseed

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #2903: a demo evidence row carries its control's kind, not
// evidenceKindsPool[i%len(pool)], so the rows a control holds are the kind
// its subject produces.

// TestControlEvidenceKinds_CoverEveryTitle checks that every demo control
// title maps to a seeded kind, and that the map names no title the seed
// no longer writes.
func TestControlEvidenceKinds_CoverEveryTitle(t *testing.T) {
	t.Parallel()
	s := &Seeder{scale: 1.0, clock: time.Now}
	fs := s.buildFixtures("demo-control-kinds")
	if len(fs.controls) != len(controlEvidenceKinds) {
		t.Errorf("seed writes %d controls; controlEvidenceKinds has %d entries", len(fs.controls), len(controlEvidenceKinds))
	}
	for _, c := range fs.controls {
		kind, ok := controlEvidenceKinds[c.Title]
		if !ok {
			t.Errorf("control %q has no entry in controlEvidenceKinds", c.Title)
			continue
		}
		if !slices.Contains(evidenceKindsPool, kind) {
			t.Errorf("control %q maps to %q, which is not in evidenceKindsPool", c.Title, kind)
		}
		if c.EvidenceKind != kind {
			t.Errorf("control %q: EvidenceKind = %q; want %q", c.Title, c.EvidenceKind, kind)
		}
	}
}

// TestBuildFixtures_EvidenceCarriesItsControlsKind checks every evidence
// row against its control, and pins the controls the S3 check (#2825) and
// the MFA check read.
func TestBuildFixtures_EvidenceCarriesItsControlsKind(t *testing.T) {
	t.Parallel()
	for _, scale := range []float64{MinScale, 0.2, DefaultScale, 2.0} {
		s := &Seeder{scale: scale, clock: time.Now}
		fs := s.buildFixtures("demo-row-kinds")
		byID := map[uuid.UUID]controlFixture{}
		for _, c := range fs.controls {
			byID[c.ID] = c
		}
		for i, e := range fs.evidence {
			c, ok := byID[e.ControlID]
			if !ok {
				t.Fatalf("scale %v: evidence[%d] is bound to an unknown control", scale, i)
			}
			if e.EvidenceKind != c.EvidenceKind {
				t.Errorf("scale %v: evidence[%d] on %q has kind %q; want the control's %q",
					scale, i, c.Title, e.EvidenceKind, c.EvidenceKind)
			}
		}
	}

	s := &Seeder{scale: 1.0, clock: time.Now}
	fs := s.buildFixtures("demo-row-kinds-pinned")
	kindsOn := map[string]map[string]bool{}
	for _, c := range fs.controls {
		kindsOn[c.Title] = map[string]bool{}
		for _, e := range fs.evidence {
			if e.ControlID == c.ID {
				kindsOn[c.Title][e.EvidenceKind] = true
			}
		}
	}
	for title, want := range map[string]string{
		"Encryption-at-rest on every data store":                    "aws.s3.bucket_encryption_state.v1",
		"Multi-factor authentication required for all human access": "okta.mfa_policy.v2",
		"Branch protection on default branches":                     "github.repo_protection.v2",
	} {
		got := kindsOn[title]
		if len(got) != 1 || !got[want] {
			t.Errorf("control %q holds kinds %v; want only %s", title, got, want)
		}
	}
}
