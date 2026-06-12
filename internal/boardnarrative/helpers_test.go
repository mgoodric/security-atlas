// Slice 440 — pure-Go unit branches for the board-narrative helpers (slice 353
// pure-Go pre-DB convention). Fast table tests for the validators / formatters
// that do not need Postgres; the Store's DB methods are covered by the
// integration tier.
package boardnarrative

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/board"
)

func TestBoundExcerpt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"under-limit-unchanged", "short", 240, "short"},
		{"exact-limit-unchanged", "abcde", 5, "abcde"},
		{"over-limit-truncated", "abcdefgh", 5, "abcde…"},
		{"multibyte-safe", "héllo wörld", 5, "héllo…"},
		{"empty", "", 240, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := boundExcerpt(tc.in, tc.n); got != tc.want {
				t.Fatalf("boundExcerpt(%q,%d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestRollupFromBrief_NoFrameworks(t *testing.T) {
	t.Parallel()
	_, err := RollupFromBrief(board.Brief{}, nil)
	if err != ErrNoBriefData {
		t.Fatalf("empty brief: want ErrNoBriefData, got %v", err)
	}
}

func TestRollupFromBrief_BoundsExcerpts(t *testing.T) {
	t.Parallel()
	b := board.Brief{
		PeriodEnd:  "2026-05-31",
		Frameworks: []board.FrameworkPosture{{Name: "SOC 2", CoveragePct: 80, FreshnessPct: 90}},
		Drift:      board.DriftSummary{WindowDays: 30, Delta: 1, FlippedOutCount: 0},
	}
	xs := make([]Excerpt, maxCitedExcerpts+5)
	for i := range xs {
		xs[i] = Excerpt{ID: "id", Kind: KindControl, Title: "t"}
	}
	r, err := RollupFromBrief(b, xs)
	if err != nil {
		t.Fatalf("RollupFromBrief: %v", err)
	}
	if len(r.Excerpts) != maxCitedExcerpts {
		t.Fatalf("excerpts not bounded: got %d, want %d", len(r.Excerpts), maxCitedExcerpts)
	}
	if r.FrameworkCount != 1 || r.CoveragePct != 80 || r.FreshnessPct != 90 {
		t.Fatalf("rollup projection wrong: %+v", r)
	}
}

func TestIsCloudProvider(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "ollama", "ollama-local", "local", "stub", "OLLAMA"} {
		if isCloudProvider(p) {
			t.Errorf("isCloudProvider(%q) = true, want false (local)", p)
		}
	}
	for _, p := range []string{"anthropic", "openai", "bedrock"} {
		if !isCloudProvider(p) {
			t.Errorf("isCloudProvider(%q) = false, want true (cloud)", p)
		}
	}
}

func TestBannedPhraseListForPrompt(t *testing.T) {
	t.Parallel()
	got := BannedPhraseListForPrompt()
	if !strings.Contains(got, "we are proud to report") {
		t.Fatalf("ban list missing a known phrase:\n%s", got)
	}
	// Every line is a bullet.
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasPrefix(line, "  - ") {
			t.Fatalf("ban-list line not a bullet: %q", line)
		}
	}
}

func TestSortExcerpts(t *testing.T) {
	t.Parallel()
	xs := []Excerpt{
		{ID: "z", Kind: KindEvidence},
		{ID: "a", Kind: KindEvidence},
		{ID: "m", Kind: KindControl},
	}
	sortExcerpts(xs)
	// control sorts before evidence; within a kind, by id ascending.
	if xs[0].Kind != KindControl || xs[1].ID != "a" || xs[2].ID != "z" {
		t.Fatalf("sortExcerpts order wrong: %+v", xs)
	}
}

func TestAllowedExcerptIDs(t *testing.T) {
	t.Parallel()
	r := Rollup{Excerpts: []Excerpt{
		{ID: "c1", Kind: KindControl},
		{ID: "e1", Kind: KindEvidence},
	}}
	got := r.allowedExcerptIDs()
	if got["c1"] != KindControl || got["e1"] != KindEvidence {
		t.Fatalf("allowedExcerptIDs wrong: %+v", got)
	}
	if _, ok := got["nope"]; ok {
		t.Fatalf("allowedExcerptIDs should not contain absent id")
	}
}
