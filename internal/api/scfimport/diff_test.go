package scfimport

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestAnchorContentEqual_SubtopicsCompareByValue(t *testing.T) {
	t.Parallel()
	ctl := Control{SCFID: "IAC-06", Family: "IAC", Title: "MFA", Subtopics: []Subtopic{{ID: "a", Text: "b"}}}
	// Postgres re-serializes JSONB with spaces and its own key order.
	row := dbx.ScfAnchor{ScfID: "IAC-06", Family: "IAC", Title: "MFA", Subtopics: []byte(`[{"id": "a", "text": "b"}]`)}
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
