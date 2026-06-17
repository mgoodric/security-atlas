package boardnarrative

import (
	"regexp"
	"strings"
)

// Tone enforcement — guardrail 7 (the banned-phrase discipline). The
// board-narrative voice is measured, factual, slightly defensive — never
// marketing voice, never unprompted superlatives the data does not earn
// (CLAUDE.md §"Tone discipline"; the canonical full list lives at
// docs/governance/board-narrative-tone-anti-patterns.md, slice 182).
//
// Two layers, belt-and-suspenders:
//  1. The system prompt INSTRUCTS the model to avoid these phrases (prompt.go
//     embeds the list). This is the primary defense.
//  2. This post-generation check is the SAFETY NET: a draft containing a banned
//     phrase is rejected before the operator sees it (P0-440-6). The model's
//     instruction-following is not trusted to be perfect — the asymmetric
//     hallucination cost of a board narrative demands the deterministic gate.
//
// bannedPhrases is the exact-match (case-insensitive) list from Section 1 of
// the governance reference. The phrases here are the UNAMBIGUOUS ones — phrases
// with no legitimate use in a board narrative — so an exact-match reject has no
// false-positive risk. The context-sensitive words ("robust", "leverage",
// "strong", "improve" without a number, ...) carry legitimate forms (Section 3
// carve-outs); rather than risk over-firing on those in v0, they are enforced
// by the system-prompt instruction + human review, and the post-generation gate
// covers the unambiguous list. This is the decisions-log JUDGMENT call on
// citation/tone strictness: exact-match the phrases that can never be right;
// instruct + review the words that sometimes are.
//
// Sourced verbatim from docs/governance/board-narrative-tone-anti-patterns.md
// Section 1 (the entries with no Section 3 carve-out) plus the apostrophe
// variant of #1.
var bannedPhrases = []string{
	"we are proud to report",
	"we're proud to report",
	"proud to report",
	"exceeded expectations",
	"industry-leading",
	"best-in-class",
	"world-class",
	"seamlessly",
	"mission-critical",
	"cutting-edge",
	"at the forefront",
	"synergy",
	"synergies",
	"going forward",
	"at this point in time",
	"move the needle",
	"paradigm shift",
	"we have built a culture of",
	// Section 1 #8 — the unprompted-superlative catch-all enumerated.
	"unprecedented",
	"revolutionary",
	"groundbreaking",
	"transformative",
	"state-of-the-art",
}

// containsBannedPhrase reports whether the draft contains any banned phrase
// (case-insensitive). The first match short-circuits — a single banned phrase
// fails the whole draft (guardrail 7), the same all-or-nothing discipline as
// the citation + numeric gates.
//
// Two layers (AC-5 + AC-6):
//
//  1. The Section-1 unambiguous list (bannedPhrases) — phrases with NO
//     legitimate use in a board narrative. Plain case-insensitive Contains; no
//     false-positive risk.
//  2. The Section-3 context-sensitive words (carveOutWords) — words like
//     "robust" / "leverage" / "strong" / "improve" that have BOTH a banned
//     filler form and a permitted form (the slice-182 Section-3 allow-list). For
//     these we reject ONLY when the word appears in its banned form and is NOT
//     covered by an allow-list pattern, so "robust against unauthorized merges"
//     (permitted) passes while "we have a robust program" (filler) is rejected
//     (P0-501-3 / P0-501-4 — honor the allow-list, no false-reject).
func containsBannedPhrase(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range bannedPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	for _, w := range carveOutWords {
		if w.banned(lower) {
			return true
		}
	}
	return false
}

// carveOutWord is one slice-182 Section-3 context-sensitive word: a word that is
// banned as filler but PERMITTED in a specific, legitimate form. The
// post-generation gate must reject the filler form WITHOUT false-rejecting the
// permitted form (AC-6). Each word ships:
//
//   - word: the bare word (lower-case), the thing we look for at all;
//   - permitted: a regex matching the LEGITIMATE forms (Section-3 allow-list);
//     any occurrence matched by this is exempt from rejection;
//   - banned: a regex matching the FILLER forms (Section-3 "NOT OK when…").
//
// Reject logic (allowList-honoring): the word triggers a rejection iff a
// FILLER-form occurrence exists. We do NOT reject merely because the word is
// present (that would over-fire on the permitted form); we reject only the
// filler patterns. This is the deliberate "instruct + review the words that
// sometimes are right; exact-match the phrases that can never be" strictness
// (decisions log) made concrete for the carve-out words.
type carveOutWord struct {
	word   string
	filler *regexp.Regexp
}

// banned reports whether the word appears in a banned (filler) form in lower
// (already lower-cased). The allow-list is honored implicitly: the filler regex
// matches ONLY the illegitimate forms enumerated in slice-182 Section-3, so a
// permitted usage (e.g. "robust against unauthorized merges") never matches.
func (c carveOutWord) banned(lower string) bool {
	if !strings.Contains(lower, c.word) {
		return false
	}
	return c.filler.MatchString(lower)
}

// carveOutWords are the slice-182 Section-3 context-sensitive words. The filler
// regex for each encodes the "NOT OK when…" column; the implicit complement (any
// other usage, including the "OK when…" forms) is permitted. Sourced from
// docs/governance/board-narrative-tone-anti-patterns.md Section 3.
//
//   - robust (P1):   banned as "robust <abstract noun>" filler ("robust program",
//     "robust controls", "robust posture"); permitted as "robust against …".
//   - leverage (P7 / Section-1 #7): banned as the verb "leverage …"; the noun
//     "leverage" is rare in this domain, so the verb form is the filler.
//   - strong (P3):   banned as "strong <abstract noun>" / "strong commitment";
//     permitted when quantified ("strong … (94% vs 88%)") — the bare
//     intensifier-before-a-noun is the filler.
//   - mature/maturity (P9): banned as "mature <noun>" editorial qualifier.
//   - comprehensive (P7): banned as "comprehensive <noun>" intensifier.
//
// The filler patterns are intentionally conservative (anchored to the specific
// illegitimate constructions) so the gate never false-rejects a permitted form —
// the human operator + the system-prompt instruction catch any residue, exactly
// the slice-182 division of labor.
var carveOutWords = []carveOutWord{
	// NOTE: Go's regexp (RE2) has NO lookaround. The filler patterns are
	// written WITHOUT lookahead: each enumerates the specific illegitimate
	// "<word> <abstract-noun>" constructions from Section 3's "NOT OK when…"
	// column. The permitted forms ("robust against …", "leverage" as a noun,
	// "strong … (94% vs 88%)", a cited maturity tier, "comprehensive … coverage
	// of …") do NOT match these patterns, so they are not false-rejected (AC-6).
	{
		word:   "robust",
		filler: regexp.MustCompile(`robust\s+(program|controls?|posture|security|solution|framework)`),
	},
	{
		word:   "leverage",
		filler: regexp.MustCompile(`\bleverage\s+(the|our|its|their|a|an)\b`),
	},
	{
		word:   "strong",
		filler: regexp.MustCompile(`strong\s+(security|commitment|posture|program|controls?|culture)`),
	},
	{
		word:   "mature",
		filler: regexp.MustCompile(`mature\s+(security\s+)?(program|posture|organization|practice)`),
	},
	{
		word:   "comprehensive",
		filler: regexp.MustCompile(`comprehensive\s+(solution|program|approach)`),
	},
}

// BannedPhraseListForPrompt renders the banned-phrase list as a newline bullet
// block for embedding in the system prompt (the guardrail-7 wiring — the list
// is instructed to the model, not only enforced post-hoc). Exported so a test
// can assert the system prompt actually contains the ban list (the
// tone-ban-list grep the orchestrator verifies).
func BannedPhraseListForPrompt() string {
	var b strings.Builder
	for _, p := range bannedPhrases {
		b.WriteString("  - ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}
