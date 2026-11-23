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
	"security_awareness.training_completion.v1",
}

// without returns kinds minus drop, in order.
func without(kinds []string, drop ...string) []string {
	out := []string{}
	for _, k := range kinds {
		if !slices.Contains(drop, k) {
			out = append(out, k)
		}
	}
	return out
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

// testCutovers puts sast's own cutover a day after the first.
func testCutovers() (Cutovers, time.Time, time.Time) {
	first := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	sast := first.Add(24 * time.Hour)
	return Cutovers{First: first, Late: map[string]time.Time{"sast.scan_result.v1": sast}}, first, sast
}

func TestKindsAsOfPinsReadsAnchoredBeforeCutover(t *testing.T) {
	t.Parallel()
	c, cut, sastCut := testCutovers()
	firstOnly := append(unbound.Kinds(), without(Kinds(), "sast.scan_result.v1")...)
	for _, tc := range []struct {
		name   string
		anchor time.Time
		want   []string
	}{
		{"unanchored (open period, live read)", time.Time{}, ControlFreeKinds()},
		{"before the cutover", cut.Add(-time.Nanosecond), unbound.Kinds()},
		{"at the cutover", cut, firstOnly},
		{"after the cutover", cut.Add(time.Hour), firstOnly},
		// #2973: a late-routed kind joins only at its own cutover.
		{"just before sast's cutover", sastCut.Add(-time.Nanosecond), firstOnly},
		{"at sast's cutover", sastCut, ControlFreeKinds()},
		{"after sast's cutover", sastCut.Add(time.Hour), ControlFreeKinds()},
	} {
		if got := KindsAsOf(c, tc.anchor); !slices.Equal(got, tc.want) {
			t.Errorf("%s: KindsAsOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPopulationKindsAtAnchorsOnTheEarlierOfCreatedAndFrozen(t *testing.T) {
	t.Parallel()
	c, cut, sastCut := testCutovers()
	firstOnly := append(unbound.Kinds(), without(Kinds(), "sast.scan_result.v1")...)
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	for _, tc := range []struct {
		name string
		pop  dbx.Population
		want []string
	}{
		{"created before, never frozen", dbx.Population{CreatedAt: ts(cut.Add(-time.Hour))}, unbound.Kinds()},
		{"created after, never frozen", dbx.Population{CreatedAt: ts(cut.Add(time.Hour))}, firstOnly},
		{"created after, frozen before (attached to an old period)",
			dbx.Population{CreatedAt: ts(cut.Add(time.Hour)), FrozenAt: ts(cut.Add(-time.Hour))}, unbound.Kinds()},
		{"created after, frozen after", dbx.Population{CreatedAt: ts(cut.Add(time.Hour)), FrozenAt: ts(cut.Add(2 * time.Hour))}, firstOnly},
		{"created after sast's cutover", dbx.Population{CreatedAt: ts(sastCut.Add(time.Hour))}, ControlFreeKinds()},
		{"created after sast's cutover, frozen between the two",
			dbx.Population{CreatedAt: ts(sastCut.Add(time.Hour)), FrozenAt: ts(cut.Add(time.Hour))}, firstOnly},
	} {
		if got := PopulationKindsAt(c, tc.pop); !slices.Equal(got, tc.want) {
			t.Errorf("%s: PopulationKindsAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// #2973: every late kind is routed, and Cutover refuses a late kind with no
// row of its own rather than reading it at the first cutover.
func TestLateKindsNeedTheirOwnCutover(t *testing.T) {
	t.Parallel()
	for _, k := range lateKinds {
		if !Kind(k) {
			t.Errorf("late kind %q is not routed", k)
		}
	}
	first := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	if _, err := cutoversFrom(first, nil); err == nil {
		t.Error("cutoversFrom with no rows: want an error naming the late kind, got nil")
	}
	if _, err := cutoversFrom(first, []dbx.EvidenceRoutingKindCutover{{EvidenceKind: "sast.scan_result.v1"}}); err == nil {
		t.Error("cutoversFrom with a NULL cut_at: want an error, got nil")
	}
	late := first.Add(time.Hour)
	c, err := cutoversFrom(first, []dbx.EvidenceRoutingKindCutover{
		{EvidenceKind: "sast.scan_result.v1", CutAt: ts(late)},
		{EvidenceKind: "github.repo_protection.v2", CutAt: ts(late)}, // not late in Go: ignored
	})
	if err != nil {
		t.Fatalf("cutoversFrom: %v", err)
	}
	if got := c.For("sast.scan_result.v1"); !got.Equal(late) {
		t.Errorf("For(sast) = %s, want %s", got, late)
	}
	if got := c.For("github.repo_protection.v2"); !got.Equal(first) {
		t.Errorf("For(github.repo_protection.v2) = %s, want the first cutover %s", got, first)
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

// #2931: Within keeps only the routed kinds, so a pinned (owned-only) set
// binds an empty routed_kinds and a live set binds every routed kind.
func TestWithin(t *testing.T) {
	t.Parallel()
	if got := Within(unbound.Kinds()); got == nil || len(got) != 0 {
		t.Fatalf("Within(owned only) = %#v, want empty non-nil", got)
	}
	if got := Within(nil); got == nil || len(got) != 0 {
		t.Fatalf("Within(nil) = %#v, want empty non-nil", got)
	}
	if got := Within(ControlFreeKinds()); !slices.Equal(got, Kinds()) {
		t.Fatalf("Within(ControlFreeKinds()) = %v, want Kinds() %v", got, Kinds())
	}
	// #2973: a read pinned between the first cutover and sast's keeps the
	// control_ref leg open for sast, as it was while sast was bound.
	c, cut, _ := testCutovers()
	if got := Within(KindsAsOf(c, cut.Add(time.Hour))); slices.Contains(got, "sast.scan_result.v1") || len(got) != len(Kinds())-1 {
		t.Fatalf("Within(pinned between the cutovers) = %v, want every routed kind but sast", got)
	}
}
