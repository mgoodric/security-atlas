package evidencesummary

import (
	"fmt"
	"strings"
	"time"
)

// systemPrompt is the fixed instruction wrapping every evidence summary. The
// tone discipline mirrors the CLAUDE.md board-narrative ban list (measured,
// factual, no marketing superlatives) even though this surface is lower-risk —
// a consistent project-wide LLM voice is cheaper to maintain than a per-surface
// one. The grounding instruction is load-bearing (threat-model T): the model is
// told to summarize ONLY the provided records and to cite IDs verbatim, which is
// what makes the citation-validation gate meaningful.
const systemPrompt = `You summarize, in plain language, what one security control's CURRENT LIVE evidence collectively shows. You are a comprehension aid for the operator; your output is informational and is never an audit artifact.

Rules you must follow:
1. Summarize ONLY the evidence records given below. Do not introduce evidence, results, dates, or coverage claims that are not in the records.
2. Do not assert that the control is covered, satisfied, or compliant beyond what the records literally show. State only what the evidence demonstrates.
3. When you refer to the control or to a specific evidence record, cite it by its exact id verbatim (the canonical UUID shown), in parentheses. Cite at least the control id and the evidence records you describe.
4. Do not invent evidence ids or control ids. Only cite ids that appear below.
5. Be measured and factual. Do not use marketing language or superlatives.
6. Keep it to a short paragraph (2 to 4 sentences).`

// buildPrompt assembles the deterministic context block the model sees from the
// bounded evidence set (AC-2). The facts in the block come straight from the
// CURRENT LIVE evidence records — the model is asked to phrase them, never to
// compute or invent them.
//
// This surface does NOT persist an ai_generations row (P0-502-4), and the
// inference client only consumes the system prompt (the Context map on
// llm.GenerateRequest is recorded by the substrate's audit path, which this
// non-persisting surface never invokes). So buildPrompt returns only the text
// block — there is no structured context_inputs map to assemble.
func buildPrompt(set EvidenceSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Control: %q (id %s)\n", set.ControlTitle, set.ControlID)
	fmt.Fprintf(&b, "Showing the %d most-recent CURRENT LIVE evidence records (of %d total on record):\n",
		len(set.Records), set.TotalCount)
	for _, e := range set.Records {
		fmt.Fprintf(&b, "  - evidence id %s: kind=%s result=%s observed_at=%s\n",
			e.EvidenceID, e.EvidenceKind, e.Result, e.ObservedAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}
