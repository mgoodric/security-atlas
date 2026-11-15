package boardnarrative

import (
	"regexp"
	"strconv"
	"strings"
)

// numberPattern matches a run of digits (optionally a leading minus, optionally
// a trailing percent sign) anywhere in the model draft. This is the
// number-extraction step of guardrail 5 (numeric-claim verification): every
// numeric token the model wrote is pulled out and checked against the
// deterministic rollup.
//
// Design notes (the JUDGMENT call — decisions log):
//   - We extract INTEGERS (with optional %). The coverage section's ground
//     truth is all integers (rounded percentages + counts — see Rollup), so a
//     decimal in the draft is itself a fabrication signal: the model invented
//     precision the pre-computation does not have. A token like "84.5" is
//     captured as 84 then 5; "5" is not in AllowedNumbers for a coverage
//     section, so the decimal draft is rejected. This is intentional strictness.
//   - The leading-minus is captured so a literal "-3" is checked as -3 (the
//     rollup allows the signed delta as well as its magnitude).
//   - Bare years / ids: the section template forbids freeform dates, and the
//     period-end label is matched separately (it is NOT a free number — see
//     allowsLabelDate). Any other 4-digit run that is not a ground-truth value
//     is a fabrication and rejected. The shape template keeps the section
//     numeric-only-about-the-rollup, so this is the desired behavior.
var numberPattern = regexp.MustCompile(`-?\d+`)

// listMarkerPattern matches the leading numbered-list markers ("1. ", "  2. ",
// ...) at the start of a line. These are the section template's structure
// (validated by guardrail 6), NOT numeric claims, so they are stripped before
// numeric extraction — otherwise the item indices 1..4 would be read as
// fabricated statistics.
var listMarkerPattern = regexp.MustCompile(`(?m)^\s*\d+\.\s`)

// extractNumbers pulls every integer token from the draft, in order, as ints.
// Tokens that overflow int (absurdly long digit runs) are returned as a
// sentinel that can never be a ground-truth value, so they fail verification.
// Pure function — no IO.
func extractNumbers(text string) []int {
	matches := numberPattern.FindAllString(text, -1)
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m)
		if err != nil {
			// Overflow or malformed — bound-check by treating it as an
			// impossible value so the draft fails (508/509 CodeQL lesson: never
			// silently narrow an unbounded parse). A real percentage/count can
			// never be this.
			out = append(out, overflowSentinel)
			continue
		}
		out = append(out, n)
	}
	return out
}

// overflowSentinel is a value no legitimate coverage number can take, used so a
// digit run that overflows int fails verification rather than being dropped.
const overflowSentinel = int(^uint(0) >> 1) // math.MaxInt

// VerifyNumbers is the reusable numeric-claim verification library (AC-3) — THE
// defining board-narrative guardrail, generalized across sections (slice 501).
// Given a draft, the section's set of permitted integer values (its
// deterministic pre-computation's AllowedNumbers), and the period-end label, it
// parses EVERY number from the draft and confirms each one is a permitted value.
// A SINGLE number outside the allowed set fails the WHOLE draft: a board
// narrative with even one fabricated statistic is unacceptable, because the
// board cannot tell the fabricated number from the real ones.
//
// This is the slice-182 "numeric-verification library" deliverable: it depends
// on NOTHING section-specific — only the (string, allowed-set, label) triple —
// so every section (and every future section) consumes the identical
// auto-reject-on-mismatch extraction logic. Slice 440's one-section check
// (verifyNumbers) is now a thin wrapper that supplies the coverage rollup's
// AllowedNumbers; new sections supply their own (see sections.go).
//
// The period-end date label (a YYYY-MM-DD string in the heading) is the one
// permitted "number-shaped" token that is NOT a statistic; it is stripped before
// extraction (stripLabelDate) so the year/month/day digits do not false-positive
// as fabricated statistics. List markers + cited UUIDs are likewise stripped.
//
// Returns false (NOT an error) when a number does not match — a numeric mismatch
// is a normal suppression outcome the caller maps to ReasonNumericMismatch.
func VerifyNumbers(text string, allowed map[int]bool, periodEnd string) bool {
	// Strip the three classes of legitimate number-shaped tokens that are NOT
	// statistics before extraction:
	//   1. the leading numbered-list markers ("1. ", "2. ", ...) — they are the
	//      template's structure (validated by guardrail 6), not claims;
	//   2. cited UUIDs (the citation tokens — their hex/digit runs are ids, not
	//      claims; validated separately by guardrail 4);
	//   3. the exact period-end label.
	// What remains is the section's actual numeric claims, every one of which
	// must be a ground-truth value.
	return allowedInts(stripNonClaims(text, periodEnd), allowed)
}

// stripNonClaims removes the number-shaped tokens that are not claims: list
// markers, cited UUIDs and the exact period-end label.
func stripNonClaims(text, periodEnd string) string {
	stripped := listMarkerPattern.ReplaceAllString(text, "")
	stripped = uuidPattern.ReplaceAllString(stripped, " ")
	return stripLabelDate(stripped, periodEnd)
}

// allowedInts reports whether every integer token in text is allowed.
func allowedInts(text string, allowed map[int]bool) bool {
	for _, n := range extractNumbers(text) {
		if !allowed[n] {
			return false
		}
	}
	return true
}

// decimalPattern matches a decimal number token ("41.9", "-0.5").
var decimalPattern = regexp.MustCompile(`-?\d+\.\d+`)

// percentPattern matches an integer written as a percentage ("42%", "42 %").
// Decimals are removed before it runs, so its digits are a whole token.
var percentPattern = regexp.MustCompile(`-?\d+\s*%`)

// verifyCoverageNumbers is guardrail 5 for the coverage section (#2210). The
// coverage figure is the board pack's, which the pack prints to one decimal,
// so the integer-only check alone would either reject the true figure or
// accept a rounded one the pack never shows. On top of the shared integer
// check it requires:
//
//   - every decimal token is the coverage figure exactly as the pack renders
//     it ("41.9"), and nothing else: "42", "41.8" and "41.93" fail;
//   - every percentage is a percentage field (the coverage figure or the
//     evidence freshness rate), so a count with a "%" after it, the retired
//     tenant-wide coverage, or "0%" for a tenant with no scoped framework
//     fails even when the same integer is a legitimate count elsewhere.
func verifyCoverageNumbers(text string, r Rollup) bool {
	stripped := stripNonClaims(text, r.PeriodEnd)
	allowedDecimal := r.coverageDecimal()
	for _, d := range decimalPattern.FindAllString(stripped, -1) {
		if d != allowedDecimal {
			return false
		}
	}
	stripped = decimalPattern.ReplaceAllString(stripped, " ")
	percents := r.allowedPercents()
	for _, p := range percentPattern.FindAllString(stripped, -1) {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(p, "%")))
		if err != nil || !percents[n] {
			return false
		}
	}
	return allowedInts(stripped, r.AllowedNumbers())
}

// verifyNumbers runs guardrail 5 for the rollup's own section.
func verifyNumbers(text string, r Rollup) bool {
	return r.verifyDraftNumbers(text)
}

// stripLabelDate removes the exact period-end label (e.g. "2026-05-31") from
// the text before numeric extraction, so the date's digits are not mistaken for
// fabricated statistics. Only the EXACT ground-truth period-end string is
// stripped — an arbitrary date the model invented is NOT stripped and therefore
// (its digits) fails verification, which is correct: the model must not invent
// dates either. A blank period-end strips nothing.
func stripLabelDate(text, periodEnd string) string {
	if periodEnd == "" {
		return text
	}
	return strings.ReplaceAll(text, periodEnd, " ")
}
