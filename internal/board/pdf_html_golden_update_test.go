// Slice 348 U-3 — Golden-file updater for buildBriefHTML.
//
// This file is the `-tags=goldenupdate` build of TestBuildBriefHTML_Golden
// that REWRITES the golden file instead of asserting against it.
//
// Usage:
//
//   go test -tags=goldenupdate -run TestBuildBriefHTML_Golden \
//     ./internal/board/
//
// The update path runs INSTEAD of the assertion path (the assertion
// file has the inverse build tag `!goldenupdate`), so a single test
// name covers both paths and the build tag selects which one runs.
//
// PR review catches accidental updates because the golden file lives
// in `internal/board/testdata/board_brief.golden.html` and is part
// of the diff.

//go:build goldenupdate

package board

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildBriefHTML_Golden(t *testing.T) {
	got := buildBriefHTML(sampleStoredBrief())

	goldenPath := filepath.Join("testdata", "board_brief.golden.html")
	if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(goldenPath), err)
	}
	if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
		t.Fatalf("write golden %q: %v", goldenPath, err)
	}
	t.Logf("wrote golden %q (%d bytes)", goldenPath, len(got))
}
