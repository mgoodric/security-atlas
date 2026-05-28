// Slice 348 U-3 — Golden-file coverage for buildBriefHTML.
//
// The existing strings.Contains assertions in pdf_html_test.go catch
// missing-section regressions but do NOT catch:
//
//   * whitespace / formatting drift inside a section
//   * attribute order on an HTML tag
//   * tag nesting changes (a div becoming a section, etc.)
//   * CSS class name renames downstream of the template
//
// A golden file `testdata/board_brief.golden.html` is the
// byte-equality baseline for the canonical sampleStoredBrief() input.
// The strings.Contains tests remain — they're the "what content is
// REQUIRED to appear" assertion; the golden is the "what EXACT shape
// the output has" assertion. The two are additive, not substitutes.
//
// Updating the golden:
//
//   go test -tags=goldenupdate -run TestBuildBriefHTML_Golden \
//     ./internal/board/
//
// The build tag protects against accidental updates in normal test
// runs. PR review catches unintentional updates (the golden file is
// in the diff).
//
// This establishes the precedent for testdata/*.golden patterns in
// the project. Future templated-render targets (pack PDF, policy
// PDF, board-narrative) can adopt the same shape — see slice 334
// U-3 finding + slice 348 decisions log D-E2 for the rollout
// rationale.

//go:build !goldenupdate

package board

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildBriefHTML_Golden(t *testing.T) {
	t.Parallel()
	got := buildBriefHTML(sampleStoredBrief())

	goldenPath := filepath.Join("testdata", "board_brief.golden.html")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %q: %v (run `go test -tags=goldenupdate -run TestBuildBriefHTML_Golden ./internal/board/` to regenerate)", goldenPath, err)
	}

	if got != string(want) {
		// On mismatch surface a useful diagnostic. Avoid dumping
		// the entire HTML to the failure message — it's ~3KB and
		// most differences are localized. Surface the first
		// divergence point + byte counts.
		minLen := len(got)
		if len(want) < minLen {
			minLen = len(want)
		}
		divergeAt := -1
		for i := 0; i < minLen; i++ {
			if got[i] != want[i] {
				divergeAt = i
				break
			}
		}
		if divergeAt == -1 {
			t.Fatalf("buildBriefHTML output length drift: got=%d want=%d (one is a prefix of the other; rerun with -tags=goldenupdate to inspect)",
				len(got), len(want))
		}
		// Surface a window around the divergence — 80 chars before
		// and after — so the failure message names the section.
		start := divergeAt - 80
		if start < 0 {
			start = 0
		}
		endGot := divergeAt + 80
		if endGot > len(got) {
			endGot = len(got)
		}
		endWant := divergeAt + 80
		if endWant > len(want) {
			endWant = len(want)
		}
		t.Fatalf("buildBriefHTML golden mismatch at byte %d:\n  got [%d..%d]: %q\n  want[%d..%d]: %q\n(rerun `go test -tags=goldenupdate -run TestBuildBriefHTML_Golden ./internal/board/` to update)",
			divergeAt, start, endGot, got[start:endGot], start, endWant, string(want)[start:endWant])
	}
}
