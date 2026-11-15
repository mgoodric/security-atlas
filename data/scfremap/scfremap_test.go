package scfremap

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEmbeddedTableParses(t *testing.T) {
	t.Parallel()
	r, ok, err := Lookup("test-2026.1", "IAC-21")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok || r.To != "IAC-10" || r.Basis != "concept" || r.ToRelease != "test-2026.3" {
		t.Fatalf("IAC-21 remap = %+v ok=%v; want IAC-10 (concept) in test-2026.3", r, ok)
	}
	if got := r.Note(); got != "IAC-21 -> IAC-10 in test-2026.3 (concept)" {
		t.Fatalf("Note = %q", got)
	}
	if _, ok, _ := Lookup("test-2026.3", "IAC-21"); ok {
		t.Fatal("a remap is keyed on its from_release")
	}
}

// TestRemapTargetsExistInTheSample: every row that remaps into the bundled
// sample's release names a code the sample carries, so a re-anchoring note
// never points an operator at an anchor that is not there (#1972 review).
func TestRemapTargetsExistInTheSample(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../migrations/fixtures/scf-sample.json")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	var sample struct {
		ReleaseVersion string `json:"release_version"`
		Controls       []struct {
			SCFID string `json:"scf_id"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(b, &sample); err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	codes := make(map[string]bool, len(sample.Controls))
	for _, c := range sample.Controls {
		codes[c.SCFID] = true
	}
	rows, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checked := 0
	for _, r := range rows {
		if r.ToRelease != sample.ReleaseVersion {
			continue
		}
		checked++
		if !codes[r.To] {
			t.Errorf("remap %s: %s is not in scf-sample.json (release %s)", r.Note(), r.To, sample.ReleaseVersion)
		}
	}
	if checked == 0 {
		t.Fatalf("no remap row targets the sample's release %q", sample.ReleaseVersion)
	}
}

func TestParseRejectsBadRows(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"not json":      `{`,
		"missing field": `{"remaps":[{"from_release":"a","to_release":"b","from":"X"}]}`,
		"bad basis":     `{"remaps":[{"from_release":"a","to_release":"b","from":"X","to":"Y","basis":"guess"}]}`,
		"duplicate":     `{"remaps":[{"from_release":"a","to_release":"b","from":"X","to":"Y","basis":"legacy"},{"from_release":"a","to_release":"b","from":"X","to":"Z","basis":"legacy"}]}`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
}
