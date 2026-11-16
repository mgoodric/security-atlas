package main

import (
	"strings"
	"testing"
)

// #2026: the warning names the fields that differed, so a difference only in
// points_of_focus no longer reads "title or body differs".
func TestDifferedPhrase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fields []string
		want   string
	}{
		{nil, "title or body differs"},
		{[]string{"title"}, "title differs"},
		{[]string{"body"}, "body differs"},
		{[]string{"points_of_focus"}, "points of focus differ"},
		{[]string{"title", "points_of_focus"}, "title and points of focus differ"},
		{[]string{"title", "body", "points_of_focus"}, "title, body and points of focus differ"},
	}
	for _, tc := range cases {
		if got := differedPhrase(tc.fields); got != tc.want {
			t.Errorf("differedPhrase(%v) = %q; want %q", tc.fields, got, tc.want)
		}
	}
}

// #2026: --strict also fails an import whose target version keeps
// point-of-focus requirement rows from before the roll-up.
func TestStrictLegacyPointsOfFocusErr(t *testing.T) {
	t.Parallel()
	if err := strictLegacyPointsOfFocusErr(false, []string{"CC1.1-POF1"}); err != nil {
		t.Fatalf("lenient: err = %v", err)
	}
	if err := strictLegacyPointsOfFocusErr(true, nil); err != nil {
		t.Fatalf("strict, none: err = %v", err)
	}
	err := strictLegacyPointsOfFocusErr(true, []string{"CC1.1-POF1", "CC1.1-POF2"})
	if err == nil {
		t.Fatal("strict with legacy rows: err = nil")
	}
	for _, want := range []string{"2 point-of-focus requirement row(s)", "CC1.1-POF1, CC1.1-POF2", "--framework-version"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q; want it to contain %q", err, want)
		}
	}
}
