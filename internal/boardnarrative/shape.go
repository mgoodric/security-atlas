package boardnarrative

import (
	"regexp"
	"strings"
)

// Section-shape enforcement — guardrail 6. The model is constrained to a
// NUMBERED template; freestyle or extra-section output is rejected BEFORE the
// operator sees it. The board narrative is a structured fiduciary report, not
// free prose: the numbered shape is what lets the operator approve section by
// section and what keeps the model from prepending an editorial summary (the
// banned F5 framing from the tone reference).
//
// The control-coverage-summary section's required shape (decisions log D1):
//
//	## Control coverage summary
//	1. <coverage sentence — states CoveragePct>
//	2. <freshness sentence — states FreshnessPct>
//	3. <drift sentence — states the 30-day delta / flipped-out count>
//	4. <scope sentence — states FrameworkCount, cites control/evidence ids>
//
// enforceShape checks the STRUCTURE (the heading + exactly the expected
// numbered items, in order) — not the prose, which the numeric + citation +
// tone gates govern. A draft that adds a 5th numbered item, drops an item,
// reorders them, or omits the heading is rejected (freestyle output, P0 G6).

// sectionHeading is the required H2 heading for the coverage section. The model
// is instructed to emit it verbatim; a draft missing it is freestyle.
const sectionHeading = "## Control coverage summary"

// expectedItems is how many numbered items the coverage section template has.
// The model must emit exactly this many, numbered 1..expectedItems in order.
const expectedItems = 4

// numberedItemPattern matches a leading "N. " markdown list item at the start
// of a line (the numbered-template shape). The capture group is the item index.
var numberedItemPattern = regexp.MustCompile(`(?m)^\s*(\d+)\.\s+\S`)

// enforceShape is the guardrail-6 gate. It returns ok=true iff the draft
// conforms to the numbered coverage-section template: the exact H2 heading is
// present, and the body contains EXACTLY expectedItems numbered items numbered
// 1..expectedItems in ascending order with no gaps, duplicates, or extras.
//
// Returns ok=false (NOT an error) on any structural violation — a shape failure
// is a normal suppression outcome mapped to ReasonBadShape.
func enforceShape(text string) bool {
	if !strings.Contains(text, sectionHeading) {
		return false
	}
	matches := numberedItemPattern.FindAllStringSubmatch(text, -1)
	if len(matches) != expectedItems {
		return false
	}
	// The item indices must be exactly 1, 2, ..., expectedItems in order.
	for i, m := range matches {
		want := itoa(i + 1)
		if m[1] != want {
			return false
		}
	}
	return true
}
