package risks

import (
	"maps"
	"testing"
)

func TestValidateSavedView(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"status": "closed", "owner": "6f1b1a3e-1c3b-4a2b-9a1e-1d2c3b4a5f60",
		"treatment": "accept", "category": "vendor", "review_due": "due_soon",
		"methodology": "fair", "org_unit": "6f1b1a3e-1c3b-4a2b-9a1e-1d2c3b4a5f61",
		"review_due_from": "2026-01-01", "review_due_to": "2026-12-31",
		"sort": "-title", "include_rollups": "true", "cols": "level,created_at",
	}
	got, err := ValidateSavedView(ok)
	if err != nil {
		t.Fatalf("valid view rejected: %v", err)
	}
	if !maps.Equal(got, ok) {
		t.Fatalf("valid view changed: %#v", got)
	}
	if got, err := ValidateSavedView(map[string]string{"owner": ""}); err != nil || len(got) != 0 {
		t.Fatalf("empty value: got %#v, %v; want dropped", got, err)
	}

	for name, bad := range map[string]map[string]string{
		"unknown key":    {"family": "IAC"},
		"paging":         {"limit": "10"},
		"status":         {"status": "draft"},
		"owner":          {"owner": "someone"},
		"treatment":      {"treatment": "ignore"},
		"category":       {"category": "Not A Slug"},
		"date":           {"review_due_from": "2026-13-01"},
		"sort":           {"sort": "colour"},
		"rollups":        {"include_rollups": "maybe"},
		"unknown column": {"cols": "level,colour"},
		"empty column":   {"cols": "level,"},
	} {
		if _, err := ValidateSavedView(bad); err == nil {
			t.Errorf("%s: %#v accepted; want an error", name, bad)
		}
	}
}

func TestSanitizeSavedView_DropsInvalidEntries(t *testing.T) {
	t.Parallel()
	got := SanitizeSavedView(map[string]string{
		"treatment": "retired", "owner": "none", "cols": "gone", "family": "IAC", "sort": "title",
	})
	want := map[string]string{"owner": "none", "sort": "title"}
	if !maps.Equal(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
