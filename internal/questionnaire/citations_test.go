package questionnaire

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// #2653: the shape check on a client's citation list.
func TestParseCitations(t *testing.T) {
	t.Parallel()
	const id = "6F1C2B4E-9D3A-4C5B-8E7F-0A1B2C3D4E5F"
	got, err := ParseCitations([]any{
		map[string]any{"id": id, "type": "controls", "title": "spoofed"},
		map[string]any{"id": id, "type": "evidence"},
	})
	if err != nil {
		t.Fatalf("ParseCitations: %v", err)
	}
	want := []Citation{
		{ID: strings.ToLower(id), Type: CitationControls},
		{ID: strings.ToLower(id), Type: CitationEvidence},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("citation %d = %+v, want %+v (client title dropped, id canonical)", i, got[i], want[i])
		}
	}

	if got, err := ParseCitations(nil); err != nil || len(got) != 0 {
		t.Errorf("ParseCitations(nil) = %v, %v; want empty, nil", got, err)
	}

	tooMany := make([]any, MaxCitations+1)
	for i := range tooMany {
		tooMany[i] = map[string]any{"id": id, "type": "controls"}
	}
	bad := map[string][]any{
		"not an object":   {"controls/" + id},
		"missing id":      {map[string]any{"type": "controls"}},
		"non-uuid id":     {map[string]any{"id": "ev-01", "type": "evidence"}},
		"numeric id":      {map[string]any{"id": 7, "type": "evidence"}},
		"missing type":    {map[string]any{"id": id}},
		"unknown type":    {map[string]any{"id": id, "type": "policies"}},
		"singular type":   {map[string]any{"id": id, "type": "control"}},
		"too many":        tooMany,
		"one bad of many": {map[string]any{"id": id, "type": "controls"}, map[string]any{"id": "x", "type": "controls"}},
	}
	for name, raw := range bad {
		if _, err := ParseCitations(raw); !errors.Is(err, ErrInvalidCitation) {
			t.Errorf("%s: err = %v, want ErrInvalidCitation", name, err)
		}
	}
}

func TestEvidenceCitationTitle(t *testing.T) {
	t.Parallel()
	// 2026-11-01T23:30 in UTC-5 is 2026-11-02 in UTC: the day is UTC's.
	observed := time.Date(2026, 11, 1, 23, 30, 0, 0, time.FixedZone("EST", -5*3600))
	if got := evidenceCitationTitle("okta.workforce_policy", observed); got != "okta.workforce_policy · 2026-11-02" {
		t.Errorf("title = %q", got)
	}
	if got := evidenceCitationTitle("", observed); got != "Evidence · 2026-11-02" {
		t.Errorf("kindless title = %q", got)
	}
}
