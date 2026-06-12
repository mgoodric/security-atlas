package gapexplain

import (
	"fmt"
	"strings"
	"time"
)

// systemPrompt is the fixed instruction wrapping every gap explanation. The
// tone discipline mirrors the CLAUDE.md board-narrative ban list (measured,
// factual, no marketing superlatives) even though this surface is lower-risk —
// a consistent project-wide LLM voice is cheaper to maintain than a per-surface
// one. The grounding instruction is load-bearing (threat-model T): the model
// is told to state ONLY the rollup facts and to cite IDs verbatim, which is
// what makes the citation-validation gate meaningful.
const systemPrompt = `You explain, in plain language, why one security control is or is not in an evidence-freshness gap. You are a comprehension aid for the operator; your output is informational and is never an audit artifact.

Rules you must follow:
1. State ONLY the facts given in the rollup below. Do not introduce numbers, dates, or coverage claims that are not in the rollup.
2. When you refer to the control or to a specific piece of evidence, cite it by its exact id verbatim (the canonical UUID shown), in parentheses. Cite at least the control id.
3. Do not invent evidence ids or control ids. Only cite ids that appear in the rollup.
4. Be measured and factual. Do not use marketing language or superlatives.
5. Keep it to a short paragraph (2 to 4 sentences).`

// buildPrompt assembles the deterministic context block the model sees from
// the rollup (AC-2). The numbers in the block come straight from the rollup —
// the model is asked to phrase them, never to compute them.
//
// This surface does NOT persist an ai_generations row (P0-444-4), and the
// local-Ollama client only consumes the system prompt (the Context map on
// llm.GenerateRequest is recorded by the substrate's audit path, which this
// non-persisting surface never invokes). So buildPrompt returns only the text
// block — there is no structured context_inputs map to assemble.
func buildPrompt(r Rollup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Control: %q (id %s)\n", r.ControlTitle, r.ControlID)
	if r.FreshnessClass != "" {
		fmt.Fprintf(&b, "Freshness class: %s\n", r.FreshnessClass)
	}
	fmt.Fprintf(&b, "Currently stale (in a freshness gap): %t\n", r.IsStale)
	fmt.Fprintf(&b, "Evidence records in window: %d\n", r.EvidenceCount)
	if r.LatestObservedAt != nil {
		fmt.Fprintf(&b, "Most recent evidence observed at: %s\n", r.LatestObservedAt.UTC().Format(time.RFC3339))
	} else {
		b.WriteString("Most recent evidence observed at: (no evidence on record)\n")
	}
	if r.ValidUntil != nil {
		fmt.Fprintf(&b, "Evidence valid until: %s\n", r.ValidUntil.UTC().Format(time.RFC3339))
	}
	if len(r.Evidence) > 0 {
		b.WriteString("Cited evidence excerpts (cite these ids verbatim):\n")
		for _, e := range r.Evidence {
			fmt.Fprintf(&b, "  - evidence id %s: kind=%s result=%s observed_at=%s\n",
				e.EvidenceID, e.EvidenceKind, e.Result, e.ObservedAt.UTC().Format(time.RFC3339))
		}
	}
	return b.String()
}
