package boardnarrative

import (
	"fmt"
	"strings"
)

// systemPromptBase is the fixed instruction wrapping every coverage-section
// generation. It wires in:
//
//   - the GROUNDING instruction (guardrail 1/4): answer ONLY from the rollup +
//     cited excerpts; cite ids verbatim; do not invent ids;
//   - the NUMERIC discipline (guardrail 5): state ONLY numbers that appear in
//     the rollup; do not compute, round, or invent a number;
//   - the SECTION-SHAPE template (guardrail 6): the exact numbered structure;
//   - the TONE discipline (guardrail 7): the measured/factual voice + the
//     banned-phrase list, embedded below via BannedPhraseListForPrompt().
//
// This is the model-facing half of every guardrail; the post-generation gates
// (citations / numeric / shape / tone) are the deterministic safety net that
// does not trust the model to follow instructions perfectly.
const systemPromptBase = `You draft ONE numbered section of a board-of-directors security report: the "Control coverage summary" section. Your draft is reviewed by a human operator and is NOT shown to the board until that operator approves it.

The audience is non-technical board members who take your words at face value. Your voice is measured, factual, and slightly defensive — like a federal Inspector-General report, not a marketing deck. Report facts; never editorialize, never use superlatives the data does not earn.

Rules you MUST follow:

1. GROUNDING. Use ONLY the pre-computed rollup numbers and the cited control/evidence material provided below. Do not introduce any number, coverage claim, date, or fact that is not in the rollup.

2. NUMBERS. State ONLY numbers that appear verbatim in the rollup. Do not compute new numbers, do not round, do not estimate, do not invent a target. If the rollup says coverage is 84, write 84 — never "about 85" or "85%".

3. CITATIONS. For the scope sentence, cite the supporting control or evidence by its exact id verbatim (the canonical UUID shown in the rollup), in parentheses. Cite ONLY ids that appear in the rollup's citable material below. Do not invent ids.

4. SECTION SHAPE. Emit EXACTLY this structure and nothing else — no preamble, no summary, no extra sections, no closing remarks:

## Control coverage summary
1. <one sentence stating the program control-coverage percentage>
2. <one sentence stating the evidence-freshness percentage>
3. <one sentence stating the 30-day drift: the net change and how many controls drifted out of passing>
4. <one sentence stating how many frameworks the program runs against, citing one or more control/evidence ids from the rollup>

Emit the heading verbatim, then exactly four numbered items, numbered 1 through 4 in order.

5. TONE. Do NOT use any of these banned phrases or any unprompted superlative:
__BANNED_PHRASES__
Be plain. A sentence that sounds like a press release fails; a sentence that sounds like a clinical observation passes.`

// bannedPhrasesPlaceholder is the token in systemPromptBase that buildSystemPrompt
// replaces with the rendered ban list. A placeholder + ReplaceAll (NOT
// fmt.Sprintf) is used deliberately: the prompt contains literal "%" signs in
// its numeric examples ("85%"), which a Sprintf format string would misparse.
const bannedPhrasesPlaceholder = "__BANNED_PHRASES__"

// buildSystemPrompt renders the full system prompt with the banned-phrase list
// embedded (guardrail-7 wiring — the list is INSTRUCTED to the model, the grep
// the orchestrator runs asserts the list text is present). The list is rendered
// once per generation but is a compile-time-constant function of the package's
// bannedPhrases, so the prompt is stable for a given prompt version.
func buildSystemPrompt() string {
	return strings.ReplaceAll(systemPromptBase, bannedPhrasesPlaceholder, BannedPhraseListForPrompt())
}

// buildPrompt assembles the HYBRID context block the model sees (guardrail 1):
// the deterministic rollup numbers PLUS the bounded cited control/evidence
// excerpts. It is NOT raw evidence records (the excerpts are bounded + summarized)
// and NOT pure rollup (the citable material is attached). The model is asked to
// PHRASE the section from these and cite them, never to retrieve or compute.
func buildPrompt(r Rollup) string {
	var b strings.Builder
	b.WriteString("Pre-computed rollup (state ONLY these numbers):\n")
	fmt.Fprintf(&b, "  - period_end: %s\n", r.PeriodEnd)
	fmt.Fprintf(&b, "  - control_coverage_pct: %s\n", itoa(r.CoveragePct))
	fmt.Fprintf(&b, "  - evidence_freshness_pct: %s\n", itoa(r.FreshnessPct))
	fmt.Fprintf(&b, "  - drift_delta_30d: %s\n", itoa(r.Delta))
	fmt.Fprintf(&b, "  - controls_drifted_out: %s\n", itoa(r.FlippedOutCount))
	fmt.Fprintf(&b, "  - drift_window_days: %s\n", itoa(r.WindowDays))
	fmt.Fprintf(&b, "  - framework_count: %s\n", itoa(r.FrameworkCount))
	if len(r.FrameworkNames) > 0 {
		fmt.Fprintf(&b, "  - frameworks: %s\n", strings.Join(r.FrameworkNames, ", "))
	}
	b.WriteString("\nCitable control/evidence material (cite these ids verbatim; cite ONLY these ids):\n")
	for _, e := range r.Excerpts {
		fmt.Fprintf(&b, "  - %s id %s: %s\n", e.Kind, e.ID, oneLine(e.Title))
		if e.Excerpt != "" {
			fmt.Fprintf(&b, "      excerpt: %s\n", oneLine(e.Excerpt))
		}
	}
	return b.String()
}

// sectionContextInputs builds the structured forensic context map recorded on
// the ai_generations audit row (R-mitigation) for ANY section (slice 501). It
// dispatches on the section so each row records exactly the rollup fields that
// section grounded on plus the citable ids. The cited ids are common to every
// section.
func sectionContextInputs(section SectionKey, r Rollup) map[string]any {
	ids := make([]string, 0, len(r.Excerpts))
	for _, e := range r.Excerpts {
		ids = append(ids, string(e.Kind)+":"+e.ID)
	}
	base := map[string]any{
		"section":     string(section),
		"period_end":  r.PeriodEnd,
		"citable_ids": ids,
	}
	switch section {
	case SectionRiskPosture:
		base["open_risks_aging"] = r.RiskCount
		base["worst_residual_severity"] = r.WorstResidualSeverity
		base["oldest_risk_age_days"] = r.OldestRiskAgeDays
	case SectionDriftActivity:
		base["drift_window_days"] = r.WindowDays
		base["drift_delta_30d"] = r.Delta
		base["controls_drifted_out"] = r.FlippedOutCount
	default: // coverage
		base["control_coverage_pct"] = r.CoveragePct
		base["evidence_freshness_pct"] = r.FreshnessPct
		base["drift_delta_30d"] = r.Delta
		base["controls_drifted_out"] = r.FlippedOutCount
		base["framework_count"] = r.FrameworkCount
	}
	return base
}

// oneLine collapses whitespace so a multi-line excerpt does not break the
// line-oriented prompt block. Purely cosmetic for prompt hygiene.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
