package scfrelease_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

func loadFixture(t *testing.T, name string) []scfrelease.Anchor {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var cat struct {
		Controls []struct {
			SCFID       string `json:"scf_id"`
			Title       string `json:"title"`
			LegacySCFID string `json:"legacy_scf_id"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(data, &cat); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	out := make([]scfrelease.Anchor, 0, len(cat.Controls))
	for _, c := range cat.Controls {
		out = append(out, scfrelease.Anchor{Code: c.SCFID, Title: c.Title, LegacyCode: c.LegacySCFID})
	}
	return out
}

// The two fixture releases produce every class (#1770 AC: unchanged,
// renamed, split, reused, withdrawn; renumbered falls out of the reuse).
func TestClassify_FixturesProduceEveryClass(t *testing.T) {
	t.Parallel()
	got := scfrelease.Classify(loadFixture(t, "scf-release-a.json"), loadFixture(t, "scf-release-b.json"))
	want := map[string]scfrelease.Kind{
		"IAC-06": scfrelease.KindUnchanged,
		"GOV-01": scfrelease.KindRenamed,
		"CRY-03": scfrelease.KindSplit,
		"AST-04": scfrelease.KindReused,
		"AST-09": scfrelease.KindRenumbered,
		"PRM-07": scfrelease.KindWithdrawn,
	}
	if len(got) != len(want) {
		t.Fatalf("classified %d codes; want %d: %v", len(got), len(want), got)
	}
	for code, kind := range want {
		if got[code].Kind != kind {
			t.Errorf("%s: kind %q; want %q", code, got[code].Kind, kind)
		}
		if got[code].Kind.Changed() != (kind != scfrelease.KindUnchanged) {
			t.Errorf("%s: Changed() = %v", code, got[code].Kind.Changed())
		}
	}
	if got["GOV-01"].Later.Title != "Cybersecurity & Data Privacy Governance Program" {
		t.Errorf("GOV-01 later title = %q", got["GOV-01"].Later.Title)
	}
}

// A later anchor that does not say its legacy code counts as the same
// lineage: only the title decides.
func TestClassify_UnstatedLegacyIsSameLineage(t *testing.T) {
	t.Parallel()
	earlier := []scfrelease.Anchor{{Code: "X-1", Title: "T"}, {Code: "X-2", Title: "U"}}
	later := []scfrelease.Anchor{{Code: "X-1", Title: "T"}, {Code: "X-2", Title: "U2"}}
	got := scfrelease.Classify(earlier, later)
	if got["X-1"].Kind != scfrelease.KindUnchanged || got["X-2"].Kind != scfrelease.KindRenamed {
		t.Fatalf("got %v", got)
	}
}
