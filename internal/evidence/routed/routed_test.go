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

// #2973 review: every routed kind resolves through Cutovers.For, given the
// rows the migrations write (one per late kind). A kind routed from its
// first record, such as one a later connector adds, needs no row and reads
// the first cutover; only a kind in lateKinds reads its own.
func TestEveryRoutedKindResolvesItsCutover(t *testing.T) {
	t.Parallel()
	first := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	late := first.Add(48 * time.Hour)
	var rows []dbx.EvidenceRoutingKindCutover
	for _, k := range lateKinds {
		rows = append(rows, dbx.EvidenceRoutingKindCutover{EvidenceKind: k, CutAt: pgtype.Timestamptz{Time: late, Valid: true}})
	}
	c, err := cutoversFrom(first, rows)
	if err != nil {
		t.Fatalf("cutoversFrom with a row per late kind: %v", err)
	}
	for _, k := range Kinds() {
		want := first
		if slices.Contains(lateKinds, k) {
			want = late
		}
		if got := c.For(k); got.IsZero() || !got.Equal(want) {
			t.Errorf("For(%q) = %s, want %s", k, got, want)
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
	c, err := cutoversFrom(first, []dbx.EvidenceRoutingKindCutover{{EvidenceKind: "sast.scan_result.v1", CutAt: ts(late)}})
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

// #3019: a cutover row for a kind not in lateKinds is an error, not ignored.
// A migration that routed a kind late without listing it in Go would
// otherwise leave the kind reading the first cutover.
func TestCutoverRowForAKindNotLateIsRefused(t *testing.T) {
	t.Parallel()
	first := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	for _, extra := range []dbx.EvidenceRoutingKindCutover{
		{EvidenceKind: "github.repo_protection.v2", CutAt: ts(first.Add(time.Hour))},   // routed at the first cutover
		{EvidenceKind: "monitoring.host_capacity.v1", CutAt: ts(first.Add(time.Hour))}, // routed from its first record
		{EvidenceKind: "manual.upload.v1", CutAt: ts(first.Add(time.Hour))},            // not routed
		{EvidenceKind: "github.repo_protection.v2"},                                    // NULL cut_at
	} {
		rows := []dbx.EvidenceRoutingKindCutover{{EvidenceKind: "sast.scan_result.v1", CutAt: ts(first.Add(time.Hour))}, extra}
		if _, err := cutoversFrom(first, rows); err == nil {
			t.Errorf("cutoversFrom with a row for %q: want an error, got nil", extra.EvidenceKind)
		}
	}
}

// firstCutoverKinds is a snapshot of the kinds routed when the first cutover
// (evidence_routing_cutover, migration 20261290000000, #2924) applied. It is
// history: never add to it. 1password.org_policy.v1 has since been replaced
// by v2 and is no longer routed.
var firstCutoverKinds = []string{
	"1password.org_policy.v1",
	"aws.s3.bucket_encryption_state.v1",
	"github.audit_event.v1",
	"github.repo_protection.v1",
	"github.repo_protection.v2",
	"jira.ticket_evidence.v1",
	"k8s.networkpolicy_coverage.v1",
	"okta.app_assignment.v1",
	"okta.mfa_policy.v2",
	"okta.user_lifecycle.v1",
	"osquery.host_posture.v1",
	"slack.retention_settings.v1",
}

// #3019: every routed kind is in exactly one class. A kind added to kinds
// after the first cutover must go in lateKinds (with a cutover row) or in
// firstRecordKinds (with a one-line justification); it cannot fall back to
// the first cutover by omission, which would silently widen periods frozen
// between the two cutovers.
func TestEveryRoutedKindIsClassified(t *testing.T) {
	t.Parallel()
	classes := map[string][]string{
		"first cutover":    firstCutoverKinds,
		"lateKinds":        lateKinds,
		"firstRecordKinds": firstRecordKinds,
	}
	for _, k := range Kinds() {
		var in []string
		for name, ks := range classes {
			if slices.Contains(ks, k) {
				in = append(in, name)
			}
		}
		switch len(in) {
		case 0:
			t.Errorf("routed kind %q is unclassified: add it to lateKinds (with an evidence_routing_kind_cutover row) or to firstRecordKinds (with a one-line justification)", k)
		case 1:
		default:
			slices.Sort(in)
			t.Errorf("routed kind %q is in more than one class: %v", k, in)
		}
	}
	for _, name := range []string{"lateKinds", "firstRecordKinds"} {
		for _, k := range classes[name] {
			if !Kind(k) {
				t.Errorf("%s entry %q is not routed", name, k)
			}
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
