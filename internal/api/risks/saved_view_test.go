package risks

import (
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
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
	if _, err := ValidateSavedView(map[string]string{
		"review_due_from": "2026-06-01", "review_due_to": "2026-06-01",
		"cols": "methodology,level,org_unit,themes,created_at",
	}); err != nil {
		t.Fatalf("one-day range with every column rejected: %v", err)
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
		"dup column":     {"cols": "level,themes,level"},
		"too many cols":  {"cols": "methodology,level,org_unit,themes,created_at,level"},
		"inverted range": {"review_due_from": "2026-12-31", "review_due_to": "2026-01-01"},
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

// A view stored before #2692 refused inverted ranges keeps its other
// entries; the range pair drops out rather than failing the register now
// that GET /v1/risks refuses it (#2693).
func TestSanitizeSavedView_DropsInvertedRange(t *testing.T) {
	t.Parallel()
	got := SanitizeSavedView(map[string]string{
		"review_due_from": "2026-12-31", "review_due_to": "2026-01-01", "owner": "none",
	})
	want := map[string]string{"owner": "none"}
	if !maps.Equal(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	kept := map[string]string{"review_due_from": "2026-06-01", "review_due_to": "2026-06-01"}
	if got := SanitizeSavedView(kept); !maps.Equal(got, kept) {
		t.Fatalf("one-day range: got %#v; want kept", got)
	}
}

func TestParsePageFilter_InvertedReviewDueRange(t *testing.T) {
	t.Parallel()
	_, msg := parsePageFilter(url.Values{
		"review_due_from": {"2026-12-31"}, "review_due_to": {"2026-01-01"},
	})
	if msg != "review_due_from must not be after review_due_to" {
		t.Fatalf("inverted range: message %q", msg)
	}
	if _, msg := parsePageFilter(url.Values{
		"review_due_from": {"2026-06-01"}, "review_due_to": {"2026-06-01"},
	}); msg != "" {
		t.Fatalf("one-day range refused: %q", msg)
	}
}

// savedViewColumns must be the register chooser's OPTIONAL_COLUMNS, in
// order, or a view saved with a column the chooser offers is refused, or
// one it no longer offers is still accepted (#2693).
func TestSavedViewColumns_MatchChooser(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../../web/app/(authed)/risks/server-query.ts")
	if err != nil {
		t.Fatalf("read chooser source: %v", err)
	}
	m := regexp.MustCompile(`(?s)export const OPTIONAL_COLUMNS = \[(.*?)\] as const;`).FindSubmatch(src)
	if m == nil {
		t.Fatal("OPTIONAL_COLUMNS not found in server-query.ts")
	}
	var web []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1) {
		web = append(web, string(q[1]))
	}
	if !slices.Equal(web, savedViewColumns) {
		t.Fatalf("savedViewColumns %v; server-query.ts OPTIONAL_COLUMNS %v", savedViewColumns, web)
	}
}
