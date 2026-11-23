// sync_drift_test.go — #2910 pure-Go coverage for the drift comparison's
// whitespace normalisation. No Postgres, no build tag (slice-353 Q-2).
package policy

import (
	"strings"
	"testing"
)

func TestNormalizeDriftBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"unchanged", "# Title\n\nBody.", "# Title\n\nBody."},
		{"crlf", "# Title\r\n\r\nBody.\r\n", "# Title\n\nBody."},
		{"bare cr", "a\rb", "a\nb"},
		{"trailing spaces and tabs", "a  \t\nb \n", "a\nb"},
		{"trailing nbsp", "a \nb", "a\nb"},
		{"blank-line run", "a\n\n\n\nb", "a\n\nb"},
		{"whitespace-only lines are blank", "a\n  \n\t\n\nb", "a\n\nb"},
		{"trailing newlines", "a\n\n\n", "a"},
		{"leading and inner spaces kept", "  - item  one", "  - item  one"},
		{"fence keeps blank-line run", "a\n```\nx\n\n\ny\n```\n\n\nb", "a\n```\nx\n\n\ny\n```\n\nb"},
		{"fence keeps trailing spaces", "```go\nx := 1  \t\n```", "```go\nx := 1  \t\n```"},
		{"tilde fence", "~~~\nx \n\n\n~~~\ny ", "~~~\nx \n\n\n~~~\ny"},
		{"fence closes only on its own marker", "```\nx \n~~~\ny \n```\nz ", "```\nx \n~~~\ny \n```\nz"},
		{"closing fence at least as long", "````\nx \n```\ny \n````\nz ", "````\nx \n```\ny \n````\nz"},
		{"indented fence up to three spaces", "   ```\nx \n   ```\ny ", "   ```\nx \n   ```\ny"},
		{"four-space indent is not a fence", "    ```\nx \n\n\ny", "    ```\nx\n\ny"},
		{"unclosed fence runs to end", "```\nx \n\n\n", "```\nx "},
		{"crlf inside fence", "```\r\nx\r\n```", "```\nx\n```"},
		{"hard break kept", "a  \nb", "a  \nb"},
		{"longer hard break is two spaces", "a     \nb", "a  \nb"},
		{"single trailing space is not a hard break", "a \nb", "a\nb"},
		{"hard break before blank line is stripped", "a  \n\nb", "a\n\nb"},
		{"hard break at end is stripped", "a\nb  ", "a\nb"},
		{"heading trailing spaces stripped", "# T  \nb", "# T\nb"},
		{"tab after spaces is not a hard break", "a  \t\nb", "a\nb"},
		{"no break before a list item", "- one  \n- two  \n* three  \n+ four  \n1. five  \n2) six", "- one\n- two\n* three\n+ four\n1. five\n2) six"},
		{"no break before a heading or quote", "a  \n## b\nc  \n> d", "a\n## b\nc\n> d"},
		{"no break before a fence", "a  \n```\nx\n```", "a\n```\nx\n```"},
		{"no break before setext or rule", "a  \n===\nb  \n---\nc  \n***", "a\n===\nb\n---\nc\n***"},
		{"hard break in a list item continuation", "- one  \n  two", "- one  \n  two"},
		{"hyphenated word continues the paragraph", "a  \n-b", "a  \n-b"},
		{"number without marker continues", "a  \n2026 was", "a  \n2026 was"},
		// #3014: only what CommonMark lets interrupt a paragraph ends one.
		{"ordered item not at 1 is lazy", "a  \n2. b", "a  \n2. b"},
		{"ordered item at 1 interrupts", "a  \n1. b", "a\n1. b"},
		{"ordered item at 01 interrupts", "a  \n01) b", "a\n01) b"},
		{"empty bullet is lazy", "a  \n*", "a  \n*"},
		{"empty plus is lazy", "a  \n+", "a  \n+"},
		{"empty ordered item is lazy", "a  \n1.", "a  \n1."},
		{"equals with text is lazy", "a  \n=b", "a  \n=b"},
		{"spaced equals is not an underline", "a  \n= =", "a  \n= ="},
		{"underscore rule", "a  \n___", "a\n___"},
		{"sibling items after an ordered item", "1. a  \n2. b  \n-\n3) c", "1. a\n2. b\n-\n3) c"},
		{"indented ordered item inside an item is lazy", "- a  \n  2. b", "- a  \n  2. b"},
		{"equals after a list item is lazy", "- a  \n===", "- a  \n==="},
		{"double dash after a list item is lazy", "- a  \n--", "- a  \n--"},
		{"rule after a list item", "- a  \n---", "- a\n---"},
		{"indented equals in an item is setext", "- a  \n  ===", "- a\n  ==="},
		{"equals after an item's second paragraph is lazy", "- a\n\n  b  \n===", "- a\n\n  b  \n==="},
		{"paragraph after a closed list", "- a\n\nb  \n===", "- a\n\nb\n==="},
		{"ordered item not at 1 after a closed list", "- a\n\nb  \n2. c", "- a\n\nb  \n2. c"},
		{"lazy line after a quote keeps break", "> a  \nb", "> a  \nb"},
		{"no break after a setext underline", "a\n===  \nb", "a\n===\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeDriftBody(tc.in); got != tc.want {
				t.Errorf("normalizeDriftBody(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBodyDrifted(t *testing.T) {
	t.Parallel()
	const stored = "# Access Control\n\nAccess is reviewed quarterly.\n"
	cases := []struct {
		name  string
		page  string
		drift bool
	}{
		{"identical", stored, false},
		{"whitespace only", "# Access Control  \r\n\r\n\r\n\r\nAccess is reviewed quarterly.", false},
		{"content", "# Access Control\n\nAccess is reviewed annually.\n", true},
		{"word spacing is content", "# Access Control\n\nAccess is  reviewed quarterly.\n", true},
		{"indentation is content", "# Access Control\n\n    Access is reviewed quarterly.\n", true},
		{"paragraph split is content", "# Access Control\n\nAccess is reviewed\nquarterly.\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bodyDrifted(stored, tc.page); got != tc.drift {
				t.Errorf("bodyDrifted(stored, %q) = %v; want %v", tc.page, got, tc.drift)
			}
		})
	}
}

// TestBodyDriftedMarkdown pins #2984: inside a code fence whitespace is
// content, and a trailing two-space hard break is content, while the same
// changes outside a fence, or where no break renders, stay normalised.
func TestBodyDriftedMarkdown(t *testing.T) {
	t.Parallel()
	const stored = "# Rotation\n\nKeys rotate yearly\nby the owner.\n\n```sh\nrotate --all\n\nverify\n```\n"
	cases := []struct {
		name  string
		page  string
		drift bool
	}{
		{"identical", stored, false},
		{"whitespace outside fence", "# Rotation  \r\n\r\n\r\nKeys rotate yearly \r\nby the owner.\r\n\r\n\r\n```sh\r\nrotate --all\r\n\r\nverify\r\n```\r\n", false},
		{"blank line added inside fence", "# Rotation\n\nKeys rotate yearly\nby the owner.\n\n```sh\nrotate --all\n\n\nverify\n```\n", true},
		{"trailing space added inside fence", "# Rotation\n\nKeys rotate yearly\nby the owner.\n\n```sh\nrotate --all \n\nverify\n```\n", true},
		{"hard break added", "# Rotation\n\nKeys rotate yearly  \nby the owner.\n\n```sh\nrotate --all\n\nverify\n```\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bodyDrifted(stored, tc.page); got != tc.drift {
				t.Errorf("bodyDrifted(stored, %q) = %v; want %v", tc.page, got, tc.drift)
			}
		})
	}
}

// TestBodyDriftedBlockStart pins #3014: where the line after a two-space
// run cannot start a block, the run renders a <br>, so adding or removing
// it is drift. Where it does start one, the run renders nothing.
func TestBodyDriftedBlockStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, stored, page string
		drift              bool
	}{
		{"ordered item not at 1", "Owners:\n2. review", "Owners:  \n2. review", true},
		{"empty bullet", "Owners:\n*", "Owners:  \n*", true},
		{"empty ordered item", "Owners:\n1.", "Owners:  \n1.", true},
		{"equals with text", "Owners:\n=all", "Owners:  \n=all", true},
		{"equals after a list item", "- Owners\n===", "- Owners  \n===", true},
		{"ordered item at 1 renders no break", "Owners:\n1. review", "Owners:  \n1. review", false},
		{"setext underline renders no break", "Owners\n===", "Owners  \n===", false},
		{"ordered list padding renders no break", "1. one\n2. two\n3. three", "1. one  \n2. two  \n3. three  ", false},
		{"bullet padding renders no break", "- one\n- two", "- one  \n- two  ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bodyDrifted(tc.stored, tc.page); got != tc.drift {
				t.Errorf("bodyDrifted(%q, %q) = %v; want %v", tc.stored, tc.page, got, tc.drift)
			}
		})
	}
}

// TestBodyDriftedConverterReflow mirrors the reflow fixture in
// internal/api/policies/sync_drift_2910_integration_test.go: every line
// gains "  \r\n" and blank runs grow. None of those trailing spaces sits
// where CommonMark renders a break, so it is still not drift (#2984).
func TestBodyDriftedConverterReflow(t *testing.T) {
	t.Parallel()
	const body = "# Vendor Policy\n\nVendors are reviewed yearly.\n\n- Tier 1: annual\n- Tier 2: biennial"
	page := strings.ReplaceAll(strings.ReplaceAll(body, "\n\n", "\n\n\n"), "\n", "  \r\n") + "\r\n\r\n"
	if bodyDrifted(body, page) {
		t.Errorf("bodyDrifted(%q, %q) = true; want false", body, page)
	}
}
