package boardnarrative

import "strings"

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
func containsBannedPhrase(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range bannedPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
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
