package scfimport

import (
	"os"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestAnchorContentEqual_SubtopicsCompareByValue(t *testing.T) {
	t.Parallel()
	ctl := Control{SCFID: "IAC-37", Family: "IAC", Title: "MFA", Subtopics: []Subtopic{{ID: "a", Text: "b"}}}
	// Postgres re-serializes JSONB with spaces and its own key order.
	row := dbx.ScfAnchor{ScfID: "IAC-37", Family: "IAC", Title: "MFA", Subtopics: []byte(`[{"id": "a", "text": "b"}]`)}
	if !anchorContentEqual(row, ctl) {
		t.Fatal("JSONB-formatted subtopics should equal the same subtopics")
	}

	for _, stored := range []string{`null`, `[]`, ``} {
		row := dbx.ScfAnchor{Family: "IAC", Title: "MFA", Subtopics: []byte(stored)}
		if !anchorContentEqual(row, Control{Family: "IAC", Title: "MFA"}) {
			t.Fatalf("stored subtopics %q should equal no subtopics", stored)
		}
	}

	row.Subtopics = []byte(`[{"id": "a", "text": "changed"}]`)
	if anchorContentEqual(row, ctl) {
		t.Fatal("different subtopic text should not be equal")
	}
}

func TestDiffRelease(t *testing.T) {
	t.Parallel()
	stored := []dbx.ScfAnchor{
		{ScfID: "A-1", Family: "A", Title: "one"},
		{ScfID: "A-2", Family: "A", Title: "two"},
		{ScfID: "A-3", Family: "A", Title: "three"},
	}
	same := []Control{
		{SCFID: "A-1", Family: "A", Title: "one"},
		{SCFID: "A-2", Family: "A", Title: "two"},
		{SCFID: "A-3", Family: "A", Title: "three"},
	}
	if c := diffRelease("r1", stored, same); c != nil {
		t.Fatalf("identical content: got conflict %v", c)
	}

	differs := []Control{
		{SCFID: "A-1", Family: "A", Title: "one"},
		{SCFID: "A-2", Family: "A", Title: "TWO"},
		{SCFID: "A-4", Family: "A", Title: "four"},
	}
	c := diffRelease("r1", stored, differs)
	if c == nil {
		t.Fatal("differing content: no conflict")
	}
	msg := c.Error()
	for _, want := range []string{`"r1"`, "changed: A-2", "added: A-4", "missing: A-3", "new release label"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestLoad_LegacySCFIDIsOptional(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/cat.json"
	body := `{"schema_version":"1.0","release_version":"r1","controls":[
		{"scf_id":"IAC-06","family":"IAC","title":"MFA","legacy_scf_id":"IAC-06.1"},
		{"scf_id":"IAC-07","family":"IAC","title":"Provisioning"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Controls[0].LegacySCFID != "IAC-06.1" || cat.Controls[1].LegacySCFID != "" {
		t.Fatalf("legacy ids = %q, %q; want IAC-06.1, empty", cat.Controls[0].LegacySCFID, cat.Controls[1].LegacySCFID)
	}
}

func TestDiffRelease_LegacySCFID(t *testing.T) {
	t.Parallel()
	stored := "IAC-06.1"
	other := "IAC-99"
	for _, tc := range []struct {
		name     string
		stored   *string
		incoming string
		conflict bool
	}{
		{"stored NULL, incoming value backfills", nil, "IAC-06.1", false},
		{"stored value, incoming empty", &stored, "", false},
		{"same value", &stored, "IAC-06.1", false},
		{"different value", &other, "IAC-06.1", true},
		{"same value with surrounding whitespace", &stored, "  IAC-06.1\t", false},
		{"blank incoming is absent", &other, "   ", false},
	} {
		row := dbx.ScfAnchor{ScfID: "IAC-06", Family: "IAC", Title: "MFA", LegacyScfID: tc.stored}
		in := Control{SCFID: "IAC-06", Family: "IAC", Title: "MFA", LegacySCFID: tc.incoming}
		if got := diffRelease("r1", []dbx.ScfAnchor{row}, []Control{in}); (got != nil) != tc.conflict {
			t.Fatalf("%s: conflict = %v; want %v", tc.name, got, tc.conflict)
		}
	}
}
