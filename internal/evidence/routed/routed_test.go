package routed

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/unbound"
)

// boundKinds are the kinds a stock bundle reads whose records are bound to one
// control: a manual upload names its control, and the rest are pushed for a
// specific control by CI or by a platform domain writer.
var boundKinds = []string{
	"access_review.completion.v1",
	"manual.upload.v1",
	"sast.scan_result.v1",
	"security_awareness.training_completion.v1",
}

func TestKindReportsTheList(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"github.repo_protection.v1", "github.repo_protection.v2", "okta.mfa_policy.v2", "osquery.host_posture.v1"} {
		if !Kind(k) {
			t.Errorf("Kind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"policy.acknowledgment.v1", "manual.upload.v1", ""} {
		if Kind(k) {
			t.Errorf("Kind(%q) = true, want false", k)
		}
	}
}

func TestRoutedAndOwnedAreDisjoint(t *testing.T) {
	t.Parallel()
	for _, k := range unbound.Kinds() {
		if Kind(k) {
			t.Errorf("%q is both owned and routed", k)
		}
	}
}

func TestControlFreeKindsIsOwnedPlusRouted(t *testing.T) {
	t.Parallel()
	got := ControlFreeKinds()
	want := append(unbound.Kinds(), Kinds()...)
	if !slices.Equal(got, want) {
		t.Fatalf("ControlFreeKinds() = %v, want %v", got, want)
	}
}

func TestKindsReturnsACopy(t *testing.T) {
	t.Parallel()
	k := Kinds()
	k[0] = "widened"
	if Kind("widened") {
		t.Fatal("mutating the result of Kinds() changed the list")
	}
	c := ControlFreeKinds()
	c[0] = "widened"
	if slices.Contains(ControlFreeKinds(), "widened") {
		t.Fatal("mutating the result of ControlFreeKinds() changed the list")
	}
}

func TestKindsAsOfPinsReadsAnchoredBeforeCutover(t *testing.T) {
	t.Parallel()
	cut := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		anchor time.Time
		want   []string
	}{
		{"unanchored (open period, live read)", time.Time{}, ControlFreeKinds()},
		{"before the cutover", cut.Add(-time.Nanosecond), unbound.Kinds()},
		{"at the cutover", cut, ControlFreeKinds()},
		{"after the cutover", cut.Add(time.Hour), ControlFreeKinds()},
	} {
		if got := KindsAsOf(cut, tc.anchor); !slices.Equal(got, tc.want) {
			t.Errorf("%s: KindsAsOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPopulationKindsAtAnchorsOnTheEarlierOfCreatedAndFrozen(t *testing.T) {
	t.Parallel()
	cut := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	for _, tc := range []struct {
		name string
		pop  dbx.Population
		want []string
	}{
		{"created before, never frozen", dbx.Population{CreatedAt: ts(cut.Add(-time.Hour))}, unbound.Kinds()},
		{"created after, never frozen", dbx.Population{CreatedAt: ts(cut.Add(time.Hour))}, ControlFreeKinds()},
		{"created after, frozen before (attached to an old period)",
			dbx.Population{CreatedAt: ts(cut.Add(time.Hour)), FrozenAt: ts(cut.Add(-time.Hour))}, unbound.Kinds()},
		{"created after, frozen after", dbx.Population{CreatedAt: ts(cut.Add(time.Hour)), FrozenAt: ts(cut.Add(2 * time.Hour))}, ControlFreeKinds()},
	} {
		if got := PopulationKindsAt(cut, tc.pop); !slices.Equal(got, tc.want) {
			t.Errorf("%s: PopulationKindsAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestStockBundleKindsAreCategorized fails when a stock bundle reads a kind
// that is in no category, so a new connector kind cannot silently land bound
// to the SCF anchor in a connector flag and reach no control (#2924).
func TestStockBundleKindsAreCategorized(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("../../../controls/*/*/control.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no stock bundles found under controls/")
	}
	seen := 0
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			EvidenceQueries []struct {
				EvidenceKind string `yaml:"evidence_kind"`
			} `yaml:"evidence_queries"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for _, q := range doc.EvidenceQueries {
			seen++
			k := q.EvidenceKind
			if Kind(k) || slices.Contains(unbound.Kinds(), k) || slices.Contains(boundKinds, k) {
				continue
			}
			t.Errorf("%s reads %q, which is not routed, owned or bound", p, k)
		}
	}
	if seen == 0 {
		t.Fatal("no evidence queries parsed from the stock bundles")
	}
}
