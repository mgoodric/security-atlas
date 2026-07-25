// Slice 268 — pure unit tests for the search package (helpers only).
// Integration tests (DB-backed) live in `cross_tenant_isolation_integration_test.go`
// and `integration_test.go` (build tag `integration`).
//
// Coverage here:
//
//	- parseLimit:    default, min, max, overflow, non-numeric
//	- parseTypes:    default, single, CSV, unknown, sort-canonicalization
//	- tokenize:      whitespace + punctuation split
//	- relevance:     token-overlap math (floor, ceiling, midpoint)
//	- snippet:       short-circuit, centered window, ellipsis edges
//	- escapeLike:    %, _, \ wildcards
//
// The Handle method's HTTP-surface tests (400 / 200 / partial_types
// shape) live in the integration test file alongside the two-tenant
// RLS coverage so they exercise the full router + middleware chain.

package search

import (
	"strings"
	"testing"
)

func TestParseLimit_Default(t *testing.T) {
	n, err := parseLimit("")
	if err != nil {
		t.Fatalf("parseLimit(\"\"): %v", err)
	}
	if n != DefaultLimit {
		t.Errorf("parseLimit(\"\") = %d, want %d", n, DefaultLimit)
	}
}

func TestParseLimit_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1", 1},
		{"10", 10},
		{"50", 50}, // MaxLimit boundary
	}
	for _, c := range cases {
		got, err := parseLimit(c.in)
		if err != nil {
			t.Errorf("parseLimit(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseLimit_Rejects(t *testing.T) {
	cases := []struct {
		in       string
		wantHint string
	}{
		{"0", "≥ 1"},
		{"-5", "≥ 1"}, // Sscanf accepts "-5" → -5; below 1 trips ≥ 1
		{"51", "≤ 50"},
		{"abc", "integer"},
		{"50000", "≤ 50"},
	}
	for _, c := range cases {
		_, err := parseLimit(c.in)
		if err == nil {
			t.Errorf("parseLimit(%q) accepted, want error", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.wantHint) {
			t.Errorf("parseLimit(%q) err = %q, want substring %q",
				c.in, err.Error(), c.wantHint)
		}
	}
}

func TestParseTypes_Default(t *testing.T) {
	got, err := parseTypes("")
	if err != nil {
		t.Fatalf("parseTypes(\"\"): %v", err)
	}
	// Default is the canonical-sorted set of all four (slice 661 added
	// `anchors`, which sorts first alphabetically).
	wantSlice := []string{TypeAnchors, TypeControls, TypeEvidence, TypeRisks}
	if !equalStringSlices(got, wantSlice) {
		t.Errorf("parseTypes(\"\") = %v, want %v", got, wantSlice)
	}
}

// TestParseTypes_AnchorsAdmitted pins the slice-661 admit-map addition:
// `anchors` is a valid `types` value and survives the canonical sort
// (it sorts first, ahead of controls).
func TestParseTypes_AnchorsAdmitted(t *testing.T) {
	got, err := parseTypes("controls,anchors")
	if err != nil {
		t.Fatalf("parseTypes(\"controls,anchors\"): %v", err)
	}
	want := []string{TypeAnchors, TypeControls}
	if !equalStringSlices(got, want) {
		t.Errorf("parseTypes(\"controls,anchors\") = %v, want %v", got, want)
	}
}

func TestParseTypes_Subset(t *testing.T) {
	got, err := parseTypes("risks,controls")
	if err != nil {
		t.Fatalf("parseTypes: %v", err)
	}
	// Order MUST be canonical regardless of input order: controls
	// (alphabetically first) then risks. Evidence is absent.
	want := []string{TypeControls, TypeRisks}
	if !equalStringSlices(got, want) {
		t.Errorf("parseTypes(\"risks,controls\") = %v, want %v", got, want)
	}
}

func TestParseTypes_DedupesAndTrims(t *testing.T) {
	got, err := parseTypes(" controls , controls , risks ")
	if err != nil {
		t.Fatalf("parseTypes: %v", err)
	}
	want := []string{TypeControls, TypeRisks}
	if !equalStringSlices(got, want) {
		t.Errorf("parseTypes dedupe got %v, want %v", got, want)
	}
}

func TestParseTypes_Rejects(t *testing.T) {
	cases := []string{
		"unknown",
		"controls,bogus",
		"riskz",
	}
	for _, c := range cases {
		_, err := parseTypes(c)
		if err == nil {
			t.Errorf("parseTypes(%q) accepted, want error", c)
		}
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"  multi   space ", []string{"multi", "space"}},
		{"comma,sep,erated", []string{"comma", "sep", "erated"}},
		{"Mixed CASE", []string{"mixed", "case"}},
		{"", nil},
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if !equalStringSlices(got, c.want) {
			t.Errorf("tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRelevance_Floor(t *testing.T) {
	// One token of three matches → 1/3.
	got := relevance([]string{"iam", "access", "review"}, "IAM permissions audit")
	want := 1.0 / 3.0
	if got != want {
		t.Errorf("relevance = %v, want %v", got, want)
	}
}

func TestRelevance_Ceiling(t *testing.T) {
	got := relevance([]string{"iam", "access"}, "IAM access review for AWS")
	if got != 1.0 {
		t.Errorf("relevance = %v, want 1.0", got)
	}
}

func TestRelevance_NoTokens(t *testing.T) {
	got := relevance(nil, "anything")
	if got != 0 {
		t.Errorf("relevance(nil) = %v, want 0", got)
	}
}

func TestSnippet_ShortHaystack(t *testing.T) {
	got := snippet("short", "any")
	if got != "short" {
		t.Errorf("snippet short = %q, want %q", got, "short")
	}
}

func TestSnippet_Centers(t *testing.T) {
	long := strings.Repeat("aaaa ", 50) // 250 chars, no match
	got := snippet(long, "zzz")
	if runeLen := len([]rune(got)); runeLen > SnippetMaxLen {
		t.Errorf("snippet runeLen = %d, want ≤ %d", runeLen, SnippetMaxLen)
	}
	// No match → prefix + ellipsis.
	if !strings.HasSuffix(got, "…") {
		t.Errorf("snippet no-match should end with ellipsis: %q", got)
	}
}

func TestSnippet_CenteredOnMatch(t *testing.T) {
	hay := strings.Repeat("a", 60) + "needle" + strings.Repeat("b", 60)
	got := snippet(hay, "needle")
	if !strings.Contains(strings.ToLower(got), "needle") {
		t.Errorf("snippet should contain match: %q", got)
	}
	if runeLen := len([]rune(got)); runeLen > SnippetMaxLen {
		t.Errorf("snippet runeLen = %d, want ≤ %d", runeLen, SnippetMaxLen)
	}
}

func TestEscapeLike(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"50%", `50\%`},
		{"foo_bar", `foo\_bar`},
		{`back\slash`, `back\\slash`},
		{"100%_pure", `100\%\_pure`},
	}
	for _, c := range cases {
		got := escapeLike(c.in)
		if got != c.want {
			t.Errorf("escapeLike(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// equalStringSlices reports slice equality. Nil and empty slices are
// treated as equal (both indicate "no elements").
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
