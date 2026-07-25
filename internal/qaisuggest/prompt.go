package qaisuggest

import (
	"fmt"
	"strings"
)

// systemPrompt is the fixed instruction wrapping every answer suggestion. The
// grounding instruction is load-bearing (threat-model T): the model is told to
// answer ONLY from the cited candidate material and to cite IDs verbatim,
// which is what makes the citation-validation gate meaningful. The tone
// discipline mirrors the CLAUDE.md board-narrative ban list (measured,
// factual, no marketing superlatives) — a consistent project-wide LLM voice.
//
// The "insufficient evidence" instruction is the no-fabricated-coverage
// guardrail spoken to the model directly: if the candidates do not support an
// answer, it must say so rather than invent coverage. The service ALSO
// enforces this structurally (an empty candidate set never reaches the model,
// and a draft citing nothing is suppressed), so this instruction is
// belt-and-suspenders, not the sole defense.
const systemPrompt = `You draft a single answer to ONE security-questionnaire question, using ONLY the candidate evidence and policy material provided below. Your draft will be reviewed by a human operator and is NOT sent to the customer until that operator approves it.

Rules you must follow:
1. Answer ONLY from the candidate material below. Do not introduce any control, coverage, certification, date, or claim that is not supported by a candidate.
2. For every factual claim, cite the supporting candidate by its exact id verbatim (the canonical UUID shown), in parentheses. An answer with no citation is not acceptable.
3. Do not invent ids. Cite ONLY ids that appear in the candidate list below.
4. If the candidate material does not support a real answer to the question, respond with exactly: INSUFFICIENT_EVIDENCE
5. Be measured and factual. Do not use marketing language, superlatives, or hedging filler.
6. Keep the answer to a short paragraph (2 to 5 sentences).`

// insufficientSentinel is the exact token the prompt asks the model to emit
// when the candidates do not support an answer. The service treats a draft
// that is exactly this sentinel (trimmed) as the insufficient-evidence outcome
// (AC-5). Distinct from the structural insufficiency check (no candidates at
// all) — this catches the case where candidates exist but the model judges
// them off-topic.
const insufficientSentinel = "INSUFFICIENT_EVIDENCE"

// isInsufficient reports whether the model's draft is the insufficient-evidence
// sentinel. Tolerant of surrounding whitespace + trailing punctuation the
// model may add, but requires the sentinel to be the substantive content (a
// real answer that merely MENTIONS the word in prose is not insufficient).
func isInsufficient(draft string) bool {
	t := strings.TrimSpace(draft)
	t = strings.TrimRight(t, ".!\n\r ")
	return strings.EqualFold(t, insufficientSentinel)
}

// buildPrompt assembles the context block the model sees: the question text
// plus the bounded candidate excerpts with their citable ids (AC-2). The
// candidates come straight from the keyword retrieval — the model is asked to
// phrase an answer from them and cite them, never to retrieve or compute.
func buildPrompt(questionText string, cands []Candidate) string {
	var b strings.Builder
	b.WriteString("Question:\n")
	b.WriteString(questionText)
	b.WriteString("\n\nCandidate material (cite these ids verbatim; cite ONLY these ids):\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "  - %s id %s: %s\n", c.Kind, c.ID, oneLine(c.Title))
		if c.Excerpt != "" {
			fmt.Fprintf(&b, "      excerpt: %s\n", oneLine(c.Excerpt))
		}
	}
	return b.String()
}

// oneLine collapses whitespace so a multi-line policy body excerpt does not
// break the line-oriented prompt block. Purely cosmetic for prompt hygiene.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
