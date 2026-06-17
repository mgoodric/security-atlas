package boardnarrative

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgoodric/security-atlas/internal/board"
)

// ----------------------------------------------------------------------------
// Slice 501 — the section SET. Scaling the slice-440 four-gate machinery from
// ONE section (control_coverage_summary) to the rollup-grounded section set.
//
// A SectionDef is the per-section configuration the section-agnostic Service
// consumes. Every field that slice 440 hardcoded for the coverage section
// (heading, item count, allowed-number set, the prompt body, the rollup) is now
// supplied by a SectionDef, so the generation pipeline (assemble rollup -> bound
// generation -> four pre-operator gates -> persist draft -> per-section approve)
// is identical across sections and inherits all seven guardrails unchanged
// (P0-501-6 — scale the proven machinery, do not add or weaken guardrails).
//
// THE CHOSEN SET (decisions log D1 — AC-2):
//
//   - control_coverage_summary  (slice 440, the canonical reference) — coverage
//     %, freshness %, 30-day drift, framework count, cites controls/evidence.
//   - risk_posture_summary      — the top open risks aging in the register:
//     how many, the worst residual severity (rounded), the oldest age in days,
//     cites the controls behind the program. Grounded in board.Brief.TopRisks.
//   - drift_activity_summary    — the 30-day control-drift window: net delta,
//     controls drifted out, window length; the audit-period-progress / KPI-
//     movement rollup-grounded section. Grounded in board.Brief.Drift.
//
// ALL three are ROLLUP-GROUNDED: every number is a value the deterministic
// board.Brief pre-computation produced, so the numeric library can pin every
// claim. FREESTYLE commentary ("asks of the board", investment narrative,
// operational color) stays HUMAN-AUTHORED in the existing templated board pack
// (P0-501-7) — it is not auto-drafted here.
//
// The numbers come from the EXISTING board.Brief data path (slice 031), so this
// slice adds NO new source-of-truth read and invents no number — it projects the
// same frozen Brief the templated board pack already consumes, under the
// caller's RLS context. The citable excerpts are the same bounded, tenant-owned
// control/evidence set the coverage section already reads (guardrail 1's bounded
// cited material, shared across sections).
// ----------------------------------------------------------------------------

// Additional section keys (slice 501). SectionControlCoverage stays in
// boardnarrative.go as the canonical v0 key.
const (
	// SectionRiskPosture summarizes the top open risks aging in the register
	// (count, worst residual severity, oldest age), grounded in
	// board.Brief.TopRisks.
	SectionRiskPosture SectionKey = "risk_posture_summary"

	// SectionDriftActivity summarizes the 30-day control-drift window (net
	// delta, controls drifted out, window length) — the audit-period-progress /
	// KPI-movement section, grounded in board.Brief.Drift.
	SectionDriftActivity SectionKey = "drift_activity_summary"
)

// SectionDef is the per-section configuration the section-agnostic Service
// consumes. It is a PURE description — no IO, no model. The rollup it produces
// is the deterministic pre-computation (guardrail 1) AND the ground truth the
// numeric library checks every claim against (guardrail 5).
type SectionDef struct {
	// Key is the section's stable key (recorded on the persisted record + audit
	// row so sections coexist in the one table without ambiguity).
	Key SectionKey

	// PromptVersion is this section's prompt-template version tag (bump on any
	// material change to its prompt / rollup shape / template). Snapshotted onto
	// the record + the ai_generations audit row (slice-182 contract).
	PromptVersion string

	// Heading is the exact H2 heading the section must emit (guardrail 6).
	Heading string

	// ExpectedItems is how many numbered items the section template requires
	// (guardrail 6 — exactly this many, 1..N in order).
	ExpectedItems int

	// buildRollup projects a board.Brief + the bounded citable excerpts into the
	// section's deterministic rollup. Pure. Returns ErrNoBriefData when there is
	// nothing to summarize.
	buildRollup func(b board.Brief, excerpts []Excerpt) (Rollup, error)

	// systemPrompt renders the section's full system prompt (grounding + numeric
	// + shape + tone instruction, with the banned-phrase list embedded). It is
	// section-specific because the shape template differs per section.
	systemPrompt func() string

	// userPrompt renders the hybrid context block the model sees (the rollup
	// numbers PLUS the bounded cited excerpts). Section-specific because each
	// section surfaces a different slice of the rollup.
	userPrompt func(r Rollup) string
}

// sectionDefs is the registry of every AI-drafted board-narrative section. The
// Service looks a section up here by key; an unknown key is a programmer error
// (ErrUnknownSection). The set is the JUDGMENT call (decisions log D1).
var sectionDefs = map[SectionKey]SectionDef{
	SectionControlCoverage: {
		Key:           SectionControlCoverage,
		PromptVersion: promptVersion, // boardnarrative-coverage-v0 (slice 440, unchanged)
		Heading:       sectionHeading,
		ExpectedItems: expectedItems,
		buildRollup:   RollupFromBrief,
		systemPrompt:  buildSystemPrompt,
		userPrompt:    buildPrompt,
	},
	SectionRiskPosture: {
		Key:           SectionRiskPosture,
		PromptVersion: "boardnarrative-risk-v0",
		Heading:       riskHeading,
		ExpectedItems: riskExpectedItems,
		buildRollup:   riskRollupFromBrief,
		systemPrompt:  buildRiskSystemPrompt,
		userPrompt:    buildRiskPrompt,
	},
	SectionDriftActivity: {
		Key:           SectionDriftActivity,
		PromptVersion: "boardnarrative-drift-v0",
		Heading:       driftHeading,
		ExpectedItems: driftExpectedItems,
		buildRollup:   driftRollupFromBrief,
		systemPrompt:  buildDriftSystemPrompt,
		userPrompt:    buildDriftPrompt,
	},
}

// AIDraftedSections is the ordered list of section keys the Service drafts, in
// the canonical board-narrative order. The multi-section generate walks this;
// the board pack ships these (approved) sections in this order. Human-authored
// sections are NOT in this list (P0-501-7).
var AIDraftedSections = []SectionKey{
	SectionControlCoverage,
	SectionRiskPosture,
	SectionDriftActivity,
}

// sectionDef looks up a section's definition. ok=false for an unknown key (a
// programmer error in the caller, surfaced as ErrUnknownSection by the Service).
func sectionDef(key SectionKey) (SectionDef, bool) {
	d, ok := sectionDefs[key]
	return d, ok
}

// RollupForSection projects a board.Brief + bounded citable excerpts into the
// deterministic rollup for the named section. It is the pure projection the
// Store uses (and exercised directly by tests) — exported so a caller can build
// a section's ground-truth rollup without a live Brief assembler. Returns
// ErrUnknownSection for a key with no SectionDef.
func RollupForSection(b board.Brief, excerpts []Excerpt, section SectionKey) (Rollup, error) {
	def, ok := sectionDef(section)
	if !ok {
		return Rollup{}, fmt.Errorf("%w: %q", ErrUnknownSection, section)
	}
	return def.buildRollup(b, excerpts)
}

// ----------------------------------------------------------------------------
// Risk-posture section (SectionRiskPosture).
//
// Grounded in board.Brief.TopRisks (the slice-031 "top-3 risks aging" data
// path). Every number the section may state is an INTEGER projected from the
// risks: the count of open risks surfaced, the WORST residual severity rounded
// to the nearest integer, and the OLDEST age in days. Residual severity is a
// float in the Brief; the section is permitted to state only its rounded integer
// (a decimal in the draft is a fabrication signal — the same strictness as the
// coverage section's integer-only discipline).
// ----------------------------------------------------------------------------

const (
	riskHeading       = "## Risk posture summary"
	riskExpectedItems = 3
)

// riskRollupFromBrief projects a board.Brief's TopRisks into the risk-posture
// Rollup. Pure — no IO. Returns ErrNoBriefData when the Brief carries no
// framework posture (the same "nothing to summarize" gate as the coverage
// section; an empty risk register is a valid posture the section reports as
// zero, NOT an error).
func riskRollupFromBrief(b board.Brief, excerpts []Excerpt) (Rollup, error) {
	if len(b.Frameworks) == 0 {
		return Rollup{}, ErrNoBriefData
	}
	bounded := excerpts
	if len(bounded) > maxCitedExcerpts {
		bounded = bounded[:maxCitedExcerpts]
	}
	r := Rollup{
		PeriodEnd:    b.PeriodEnd,
		RiskCount:    len(b.TopRisks),
		Excerpts:     bounded,
		riskSeverity: true,
	}
	for _, rk := range b.TopRisks {
		sev := roundToInt(rk.ResidualSeverity)
		if sev > r.WorstResidualSeverity {
			r.WorstResidualSeverity = sev
		}
		if rk.AgeDays > r.OldestRiskAgeDays {
			r.OldestRiskAgeDays = rk.AgeDays
		}
	}
	return r, nil
}

func buildRiskSystemPrompt() string {
	return strings.ReplaceAll(riskSystemPromptBase, bannedPhrasesPlaceholder, BannedPhraseListForPrompt())
}

const riskSystemPromptBase = `You draft ONE numbered section of a board-of-directors security report: the "Risk posture summary" section. Your draft is reviewed by a human operator and is NOT shown to the board until that operator approves it.

The audience is non-technical board members who take your words at face value. Your voice is measured, factual, and slightly defensive — like a federal Inspector-General report, not a marketing deck. Report facts; never editorialize, never use superlatives the data does not earn.

Rules you MUST follow:

1. GROUNDING. Use ONLY the pre-computed rollup numbers and the cited control/evidence material provided below. Do not introduce any number, risk count, severity, age, or fact that is not in the rollup.

2. NUMBERS. State ONLY integers that appear verbatim in the rollup. Do not compute new numbers, do not round, do not estimate, do not state a severity as a decimal. If the rollup says the worst residual severity is 12, write 12 — never "about 12.5".

3. CITATIONS. For the third sentence, cite a supporting control or evidence by its exact id verbatim (the canonical UUID shown in the rollup), in parentheses. Cite ONLY ids that appear in the rollup's citable material below. Do not invent ids.

4. SECTION SHAPE. Emit EXACTLY this structure and nothing else — no preamble, no summary, no extra sections, no closing remarks:

## Risk posture summary
1. <one sentence stating how many open risks are aging in the register>
2. <one sentence stating the worst residual severity and the oldest risk age in days>
3. <one sentence on the controls behind the program, citing one or more control/evidence ids from the rollup>

Emit the heading verbatim, then exactly three numbered items, numbered 1 through 3 in order.

5. TONE. Do NOT use any of these banned phrases or any unprompted superlative:
__BANNED_PHRASES__
Be plain. A sentence that sounds like a press release fails; a sentence that sounds like a clinical observation passes.`

func buildRiskPrompt(r Rollup) string {
	var b strings.Builder
	b.WriteString("Pre-computed rollup (state ONLY these numbers):\n")
	fmt.Fprintf(&b, "  - period_end: %s\n", r.PeriodEnd)
	fmt.Fprintf(&b, "  - open_risks_aging: %s\n", itoa(r.RiskCount))
	fmt.Fprintf(&b, "  - worst_residual_severity: %s\n", itoa(r.WorstResidualSeverity))
	fmt.Fprintf(&b, "  - oldest_risk_age_days: %s\n", itoa(r.OldestRiskAgeDays))
	writeCitableBlock(&b, r.Excerpts)
	return b.String()
}

// ----------------------------------------------------------------------------
// Drift-activity section (SectionDriftActivity) — the audit-period-progress /
// KPI-movement rollup-grounded section.
//
// Grounded in board.Brief.Drift (the slice-031 30-day drift summary). Every
// number is an integer the drift summary produced: the net delta magnitude, the
// count of controls drifted out, and the window length in days.
// ----------------------------------------------------------------------------

const (
	driftHeading       = "## Control drift activity"
	driftExpectedItems = 3
)

func driftRollupFromBrief(b board.Brief, excerpts []Excerpt) (Rollup, error) {
	if len(b.Frameworks) == 0 {
		return Rollup{}, ErrNoBriefData
	}
	bounded := excerpts
	if len(bounded) > maxCitedExcerpts {
		bounded = bounded[:maxCitedExcerpts]
	}
	return Rollup{
		PeriodEnd:       b.PeriodEnd,
		Delta:           b.Drift.Delta,
		FlippedOutCount: b.Drift.FlippedOutCount,
		WindowDays:      b.Drift.WindowDays,
		Excerpts:        bounded,
		driftOnly:       true,
	}, nil
}

func buildDriftSystemPrompt() string {
	return strings.ReplaceAll(driftSystemPromptBase, bannedPhrasesPlaceholder, BannedPhraseListForPrompt())
}

const driftSystemPromptBase = `You draft ONE numbered section of a board-of-directors security report: the "Control drift activity" section. Your draft is reviewed by a human operator and is NOT shown to the board until that operator approves it.

The audience is non-technical board members who take your words at face value. Your voice is measured, factual, and slightly defensive — like a federal Inspector-General report, not a marketing deck. Report facts; never editorialize, never use superlatives the data does not earn.

Rules you MUST follow:

1. GROUNDING. Use ONLY the pre-computed rollup numbers and the cited control/evidence material provided below. Do not introduce any number, count, or fact that is not in the rollup.

2. NUMBERS. State ONLY integers that appear verbatim in the rollup. Do not compute new numbers, do not round, do not estimate. If the rollup says the window is 30 days, write 30 — never "about a month".

3. CITATIONS. For the third sentence, cite a supporting control or evidence by its exact id verbatim (the canonical UUID shown in the rollup), in parentheses. Cite ONLY ids that appear in the rollup's citable material below. Do not invent ids.

4. SECTION SHAPE. Emit EXACTLY this structure and nothing else — no preamble, no summary, no extra sections, no closing remarks:

## Control drift activity
1. <one sentence stating the drift window length in days and the net drift over it>
2. <one sentence stating how many controls drifted out of passing in the window>
3. <one sentence on a control behind the posture, citing one or more control/evidence ids from the rollup>

Emit the heading verbatim, then exactly three numbered items, numbered 1 through 3 in order.

5. TONE. Do NOT use any of these banned phrases or any unprompted superlative:
__BANNED_PHRASES__
Be plain. A sentence that sounds like a press release fails; a sentence that sounds like a clinical observation passes.`

func buildDriftPrompt(r Rollup) string {
	var b strings.Builder
	b.WriteString("Pre-computed rollup (state ONLY these numbers):\n")
	fmt.Fprintf(&b, "  - period_end: %s\n", r.PeriodEnd)
	fmt.Fprintf(&b, "  - drift_window_days: %s\n", itoa(r.WindowDays))
	fmt.Fprintf(&b, "  - drift_delta_30d: %s\n", itoa(r.Delta))
	fmt.Fprintf(&b, "  - controls_drifted_out: %s\n", itoa(r.FlippedOutCount))
	writeCitableBlock(&b, r.Excerpts)
	return b.String()
}

// writeCitableBlock renders the shared bounded cited-material block (the same
// shape the coverage section's buildPrompt uses) so every section feeds the
// model citable ids identically (guardrail 1 / 4).
func writeCitableBlock(b *strings.Builder, excerpts []Excerpt) {
	b.WriteString("\nCitable control/evidence material (cite these ids verbatim; cite ONLY these ids):\n")
	for _, e := range excerpts {
		fmt.Fprintf(b, "  - %s id %s: %s\n", e.Kind, e.ID, oneLine(e.Title))
		if e.Excerpt != "" {
			fmt.Fprintf(b, "      excerpt: %s\n", oneLine(e.Excerpt))
		}
	}
}

// roundToInt rounds a float64 to the nearest int (half away from zero). Used to
// project the Brief's float residual severity into the integer the risk section
// is permitted to state.
func roundToInt(f float64) int {
	if f < 0 {
		return -int(-f + 0.5)
	}
	return int(f + 0.5)
}

// sortedSectionKeys returns the AI-drafted section keys in canonical order
// (stable for tests + the board pack assembly).
func sortedSectionKeys() []SectionKey {
	out := make([]SectionKey, len(AIDraftedSections))
	copy(out, AIDraftedSections)
	sort.SliceStable(out, func(i, j int) bool {
		return sectionOrder(out[i]) < sectionOrder(out[j])
	})
	return out
}

// sectionOrder gives each section its canonical position; unknown keys sort last.
func sectionOrder(k SectionKey) int {
	for i, key := range AIDraftedSections {
		if key == k {
			return i
		}
	}
	return len(AIDraftedSections)
}
