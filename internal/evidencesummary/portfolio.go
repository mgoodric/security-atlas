package evidencesummary

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// portfolioNumberPattern matches an integer token (optional leading minus)
// anywhere in the model draft — the number-extraction step of the AC-3
// numeric-claim verification (the slice-501 pattern, lifted locally per
// decisions-log D2). Decimals are captured as their integer parts, so an invented
// decimal "84.5" yields 84 then 5, at least one of which is outside the rollup
// allowed set, failing the draft (intentional strictness — the rollup is
// integer-only).
var portfolioNumberPattern = regexp.MustCompile(`-?\d+`)

// portfolioOverflowSentinel is a value no legitimate rollup count can take, used
// so a digit run that overflows int fails verification rather than being dropped
// (the slice-501 / slice-508 lesson: never silently narrow an unbounded parse).
const portfolioOverflowSentinel = int(^uint(0) >> 1) // math.MaxInt

// atoiBounded parses an integer token, returning an error on overflow/malformed
// so the caller can map it to the overflow sentinel.
func atoiBounded(s string) (int, error) { return strconv.Atoi(s) }

// portfolioPromptVersion is the portfolio surface's prompt-template version tag
// (slice-182 schema contract shape). Distinct from the single-control
// promptVersion so a forensic reconstruction can tell the surfaces apart.
const portfolioPromptVersion = "portfolio-evidencesummary-v0"

// portfolioSystemPrompt is the fixed instruction wrapping every portfolio
// summary. It mirrors the single-control systemPrompt's tone discipline +
// grounding rules, extended for the cross-control + numeric-claim shape: the model
// must use ONLY the rollup's numbers for counts and must not assert coverage
// beyond what each control's records show.
const portfolioSystemPrompt = `You summarize, in plain language, what a SET of security controls' CURRENT LIVE evidence collectively shows across a portfolio. You are a comprehension aid for the operator; your output is informational and is never an audit artifact.

Rules you must follow:
1. Summarize ONLY the controls and evidence records given below. Do not introduce controls, evidence, results, dates, or coverage claims that are not provided.
2. Do not assert that a control, or the portfolio, is covered, satisfied, or compliant beyond what the records literally show. A control with no current live evidence is a gap; never describe it as covered.
3. When you state a COUNT (how many controls, how many with evidence, how many gaps, how many records), use ONLY the deterministic rollup numbers given below. Do not compute or invent any other number.
4. When you refer to a control or a specific evidence record, cite it by its exact id verbatim (the canonical UUID shown), in parentheses. Cite the controls and evidence records you describe.
5. Do not invent evidence ids or control ids. Only cite ids that appear below.
6. Be measured and factual. Do not use marketing language or superlatives.
7. Keep it to a short paragraph (2 to 5 sentences).`

// ===== slice 750 — portfolio / multi-control evidence summary =====
//
// This file GENERALIZES the slice-502 one-control surface to a FILTERED control
// SET (by control-family or by framework). It is the cross-control sibling of
// period.go (slice 749's frozen-population sibling): it assembles a TWO-LEVEL
// bounded cross-control corpus, then runs the IDENTICAL slice-502 pipeline (one
// bounded generation, validate-every-citation-then-suppress, graceful
// degradation) via the shared runSummary machinery — plus ONE portfolio-specific
// gate the single-control surface does not need: numeric-claim verification
// against the deterministic rollup (AC-3, the slice-501 pattern), so a fabricated
// portfolio count ("40 of 40 controls covered" when the rollup shows 30)
// auto-suppresses.
//
// All slice-502 constitutional invariants are inherited verbatim (P0-750-1): no
// fabricated coverage, no cross-tenant bleed, local-default routing, current live
// evidence only, never persisted, no approve/publish/export, deterministic set
// always returned. The NEW work is the cross-control corpus bounding (the
// headline JUDGMENT call) and the portfolio numeric verification.

// MaxControlsPerSummary bounds how many controls enter one portfolio summary —
// the FIRST level of the two-level bound (P0-750-2, AC-1). The control set is
// resolved deterministically (bundle_id ASC, id ASC) and capped here, so a
// framework/family slice with hundreds of controls still produces a bounded
// prompt and a bounded citable-id set. The number is the headline JUDGMENT call
// (decisions-log D1): 12 controls is enough to read as a portfolio-level picture
// for the solo-security-leader persona's framework/family slice, while
// 12 * MaxRecordsPerControl cited excerpts + 12 control ids = 60 citable ids keeps
// the prompt well inside MaxSummaryTokens headroom and the citation gate fast.
const MaxControlsPerSummary = 12

// MaxRecordsPerControl bounds how many CURRENT LIVE evidence records per control
// enter the corpus — the SECOND level of the two-level bound (P0-750-2, AC-1).
// It is intentionally SMALLER than the single-control MaxCitedExcerpts (8): at
// portfolio scale the prompt multiplies records by controls, so each control
// contributes only its few most-recent records (recency bound, observed_at DESC).
// 4 records is enough per-control grounding to characterize the control's recent
// posture without N*M blowing up the corpus (decisions-log D1).
const MaxRecordsPerControl = 4

// ControlEvidence is one control's slice of the portfolio corpus: the control's
// identity (cited via ControlID) plus its bounded most-recent live records. It is
// the per-control unit the prompt iterates and the citation grounding set is
// built from.
type ControlEvidence struct {
	ControlID    uuid.UUID
	ControlTitle string

	// Records is the bounded set of CURRENT LIVE cited excerpts for THIS control
	// (newest-first, capped at MaxRecordsPerControl).
	Records []EvidenceFact

	// TotalCount is this control's full live evidence count (for the deterministic
	// rollup + the per-control "showing N of M" honesty). It may exceed
	// len(Records) when the history is longer than the per-control bound.
	TotalCount int
}

// PortfolioFilter names the control set a portfolio summary covers. Exactly the
// AC-1 filter dimensions: a control-family OR a framework version (resolved to
// SCF anchors by the handler). An empty filter is the whole-program rollup. Scope
// being multidimensional, the (heavier) scope-cell intersection is a documented
// follow-on, not built here.
type PortfolioFilter struct {
	// Family, when non-empty, restricts the set to controls in this control_family.
	Family string

	// FrameworkVersionID, when set, restricts the set to controls anchored on the
	// framework version's SCF anchors. The PortfolioStore resolves it to the anchor
	// id set via the existing UCF traversal.
	FrameworkVersionID uuid.UUID

	// FrameworkLabel is a human-readable label for the framework filter (slug +
	// version), surfaced in the prompt + the UI. Purely cosmetic; never a
	// grounding input.
	FrameworkLabel string
}

// Mode returns a short machine label for the filter dimension, used in the
// response so the UI can phrase the scope honestly.
func (f PortfolioFilter) Mode() string {
	switch {
	case f.FrameworkVersionID != uuid.Nil:
		return "framework"
	case f.Family != "":
		return "family"
	default:
		return "program"
	}
}

// PortfolioSet is the DETERMINISTIC two-level bounded cross-control evidence
// corpus for a filtered control set (AC-1), assembled server-side under the
// requesting tenant's RLS. It is ALWAYS returned to the caller, with or without a
// summary (AC-7, P0-502-7 carried forward).
type PortfolioSet struct {
	Filter PortfolioFilter

	// Controls is the bounded per-control corpus (capped at MaxControlsPerSummary).
	Controls []ControlEvidence

	// TotalControls is the number of controls the filter matched BEFORE the
	// controls-per-summary cap, for the "summarizing K of N controls" honesty
	// label (AC-5). It may exceed len(Controls).
	TotalControls int
}

// Rollup is the DETERMINISTIC portfolio rollup the numeric-claim gate (AC-3)
// checks the model's counts against. Every number the summary is permitted to
// state about the portfolio comes from here — never from the model. It is
// computed purely from the bounded PortfolioSet (controls with >=1 live record
// count as "with evidence"; the rest are gaps).
type Rollup struct {
	// ControlsInSummary is len(PortfolioSet.Controls) — the K in "K of N".
	ControlsInSummary int
	// TotalMatched is PortfolioSet.TotalControls — the N in "K of N".
	TotalMatched int
	// ControlsWithEvidence is how many of the in-summary controls have >=1 live
	// record. Controls without evidence are gaps, never asserted as covered.
	ControlsWithEvidence int
	// ControlsWithoutEvidence is the complement (gaps).
	ControlsWithoutEvidence int
	// TotalRecords is the count of cited excerpts across the in-summary controls.
	TotalRecords int
}

// computeRollup derives the deterministic rollup from a bounded portfolio set.
// Pure function — no IO, no model.
func computeRollup(set PortfolioSet) Rollup {
	r := Rollup{
		ControlsInSummary: len(set.Controls),
		TotalMatched:      set.TotalControls,
	}
	for _, c := range set.Controls {
		r.TotalRecords += len(c.Records)
		if len(c.Records) > 0 {
			r.ControlsWithEvidence++
		}
	}
	r.ControlsWithoutEvidence = r.ControlsInSummary - r.ControlsWithEvidence
	return r
}

// allowedNumbers is the set of integer values the portfolio summary may state
// (AC-3). It is the deterministic rollup's numbers; any number in the draft
// outside this set is a fabricated statistic and fails the whole draft (the
// slice-501 VerifyNumbers contract). We include each count and 0 (a legitimate
// "0 gaps" / "0 records" claim) — nothing else.
func (r Rollup) allowedNumbers() map[int]bool {
	allowed := map[int]bool{
		0:                         true,
		r.ControlsInSummary:       true,
		r.TotalMatched:            true,
		r.ControlsWithEvidence:    true,
		r.ControlsWithoutEvidence: true,
		r.TotalRecords:            true,
	}
	return allowed
}

// PortfolioSummary is the result of PortfolioService.PortfolioSummarize. It
// embeds the same Summary the single-control surface returns
// (Text/Citations/Suppressed/Reason/Model* — identical NON-BINDING, read-only,
// never-persisted contract) and carries the deterministic PortfolioSet + Rollup
// for the bounded-scope UI labels (AC-5).
type PortfolioSummary struct {
	Summary

	PortfolioSet PortfolioSet
	Rollup       Rollup
}

// ReasonNumericMismatch is the suppression reason when a portfolio summary states
// a count that does not match the deterministic rollup (AC-3, P0-750-3). It is in
// addition to the inherited slice-502 reasons; the slice-501 numeric-claim check
// is the gate.
const ReasonNumericMismatch = "numeric_mismatch"

// PortfolioEvidenceReader assembles the deterministic TWO-LEVEL bounded
// cross-control evidence set for a filtered control set under the caller's RLS
// context. The production implementation is *PortfolioStore; tests supply a fake
// so the bounding + suppression + numeric branches are exercised without a live
// Postgres on the unit surface (the integration tier uses the real *PortfolioStore).
type PortfolioEvidenceReader interface {
	PortfolioSet(ctx context.Context, filter PortfolioFilter) (PortfolioSet, error)
}

// PortfolioService is the slice-750 portfolio orchestrator. It is a cross-control
// variant of *Service: it assembles the two-level bounded cross-control evidence
// set, runs the IDENTICAL slice-502 pipeline (runSummary), then applies ONE extra
// portfolio gate — numeric-claim verification against the deterministic rollup
// (AC-3). It holds no per-call state and persists nothing (P0-502-4 carried
// forward).
//
// The reuse is literal: PortfolioSummarize delegates the generate + validate +
// suppress machinery to runSummary (the shared pipeline factored out of
// Service.Summarize), passing the FLATTENED cross-control EvidenceSet as the
// corpus and the cross-control resolver. The ONLY differences from the
// single-control surface are (a) the corpus is many controls' records, (b) the
// numeric gate runs after the citation gate.
type PortfolioService struct {
	reader   PortfolioEvidenceReader
	client   llm.Client
	resolver CitationResolver
}

// NewPortfolioService wires the cross-control reader, the inference client (the
// slice-499 per-tenant router in production, the Stub in CI), and the citation
// resolver. In production the reader + resolver are both backed by the same
// *PortfolioStore (the resolver IS the slice-502 Store.Resolve shape — a cited id
// resolves to ANY tenant-owned control/evidence row; the grounding gate over the
// cross-control allowed set is what scopes citations to the summarized controls).
func NewPortfolioService(reader PortfolioEvidenceReader, client llm.Client, resolver CitationResolver) *PortfolioService {
	return &PortfolioService{reader: reader, client: client, resolver: resolver}
}

// PortfolioSummarize produces the portfolio evidence summary for a filtered
// control set in the caller's tenant context. It ALWAYS returns the deterministic
// TWO-LEVEL bounded PortfolioSet + Rollup (AC-1, AC-7); the plain-language Text +
// Citations are present only when generation succeeded, EVERY citation resolved to
// a tenant-owned row in the cross-control grounding set (AC-2), AND every numeric
// claim matched the deterministic rollup (AC-3). On any failure the returned
// PortfolioSummary has Suppressed=true and a fixed Reason, and the caller renders
// the deterministic rollup + per-control evidence alone.
//
// The only error PortfolioSummarize returns is a genuine evidence-read failure
// (DB unreachable, tenant context missing). A model/citation/numeric failure is
// NOT an error: it is graceful degradation conveyed via Suppressed.
func (s *PortfolioService) PortfolioSummarize(ctx context.Context, filter PortfolioFilter) (PortfolioSummary, error) {
	set, err := s.reader.PortfolioSet(ctx, filter)
	if err != nil {
		return PortfolioSummary{}, fmt.Errorf("evidencesummary: assemble portfolio set: %w", err)
	}

	rollup := computeRollup(set)

	// Run the IDENTICAL slice-502 pipeline, but with the PORTFOLIO prompt. We
	// cannot reuse runSummary's internal buildPrompt (it is single-control), so we
	// run the generate+validate steps with the portfolio prompt here and reuse the
	// shared citation gate + suppression vocabulary, plus the portfolio numeric gate.
	sum := s.runPortfolio(ctx, set, rollup)

	return PortfolioSummary{
		Summary:      sum,
		PortfolioSet: set,
		Rollup:       rollup,
	}, nil
}

// runPortfolio is the portfolio analogue of runSummary. It builds the
// cross-control prompt, runs ONE bounded generation, applies the IDENTICAL
// validate-every-citation-then-suppress gate (over the cross-control grounding
// set), then the EXTRA numeric-claim gate. The EvidenceSet is never the carrier
// here (the PortfolioSet is returned alongside by the caller); this returns the
// Summary value with Text/Citations populated only on full success.
func (s *PortfolioService) runPortfolio(ctx context.Context, set PortfolioSet, rollup Rollup) Summary {
	var sum Summary

	// Nothing to summarize across the whole set: degrade without a model call.
	if rollup.TotalRecords == 0 {
		sum.Suppressed = true
		sum.Reason = ReasonNoEvidence
		return sum
	}

	res, err := s.client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceSummary,
		PromptVersion: portfolioPromptVersion,
		SystemPrompt:  portfolioSystemPrompt + "\n\n" + buildPortfolioPrompt(set, rollup),
		Context:       nil,
		MaxTokens:     MaxSummaryTokens,
		Timeout:       GenerationTimeout,
	})
	if err != nil {
		sum.Suppressed = true
		sum.Reason = ReasonGenerationUnavailable
		return sum
	}

	sum.ModelName = res.ModelName
	sum.ModelVersion = res.ModelVersion
	sum.ModelProvider = res.ModelProvider

	citations, ok, reason, err := validateCitations(ctx, s.resolver, res.Text, portfolioAllowedIDs(set))
	if err != nil {
		sum.Suppressed = true
		sum.Reason = ReasonUnresolvedCitation
		return sum
	}
	if !ok {
		sum.Suppressed = true
		sum.Reason = reason
		return sum
	}

	// AC-3 / P0-750-3: numeric-claim verification. Every integer the draft states
	// about the portfolio MUST be a value the deterministic rollup permits. A
	// single fabricated count fails the whole draft (the slice-501 contract). This
	// is the portfolio-specific gate the single-control surface does not need: a
	// portfolio summary is the surface where "all 40 controls covered" can be
	// asserted, so the count must be ground-truth-checked.
	if !verifyPortfolioNumbers(res.Text, rollup) {
		sum.Suppressed = true
		sum.Reason = ReasonNumericMismatch
		return sum
	}

	sum.Text = res.Text
	sum.Citations = citations
	return sum
}

// portfolioAllowedIDs builds the cross-control grounding set the citation gate is
// applied to (AC-2): the union of every summarized control id + every per-control
// cited-excerpt evidence id across the bounded set. A draft may cite any
// summarized control or any of their cited excerpts — and NOTHING else (the
// grounding gate over this set is what scopes citations to the summarized
// controls, even though the resolver itself can resolve any tenant-owned row).
func portfolioAllowedIDs(set PortfolioSet) map[uuid.UUID]bool {
	allowed := make(map[uuid.UUID]bool, len(set.Controls)*(MaxRecordsPerControl+1))
	for _, c := range set.Controls {
		allowed[c.ControlID] = true
		for _, e := range c.Records {
			allowed[e.EvidenceID] = true
		}
	}
	return allowed
}

// verifyPortfolioNumbers is the AC-3 gate over the portfolio rollup. It reuses the
// slice-501 numeric-claim-verification PATTERN (scan every integer claim, confirm
// membership in an allowed set, a single miss fails the draft). We lift the small
// pattern locally rather than importing internal/boardnarrative.VerifyNumbers, to
// avoid an evidencesummary -> boardnarrative package coupling for a ~10-line scan
// (decisions-log D2); the portfolio summary has no period-end label and no
// numbered-section template, so the board-narrative wrapper's extra stripping
// (list markers, label dates) is not needed — only the UUID strip (citation ids
// must not be read as statistics) applies.
func verifyPortfolioNumbers(text string, rollup Rollup) bool {
	// Strip cited UUIDs first so their hex/digit runs are not read as fabricated
	// statistics (they are validated separately by the citation gate).
	stripped := uuidPattern.ReplaceAllString(text, " ")
	allowed := rollup.allowedNumbers()
	for _, n := range extractPortfolioNumbers(stripped) {
		if !allowed[n] {
			return false
		}
	}
	return true
}

// extractPortfolioNumbers pulls every integer token from the (UUID-stripped)
// draft, in order. A digit run that overflows int is returned as a sentinel that
// can never be a rollup value, so it fails verification rather than being
// silently dropped (the slice-501 lesson). Pure function — no IO.
func extractPortfolioNumbers(text string) []int {
	matches := portfolioNumberPattern.FindAllString(text, -1)
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		n, err := atoiBounded(m)
		if err != nil {
			out = append(out, portfolioOverflowSentinel)
			continue
		}
		out = append(out, n)
	}
	return out
}

// sortControlsForDeterminism keeps the per-control corpus stable for the prompt +
// rollup regardless of reader iteration order. The store already orders by
// bundle_id; this guards the fake reader + any future reader from a flaky order.
func sortControlsForDeterminism(controls []ControlEvidence) {
	sort.SliceStable(controls, func(i, j int) bool {
		return controls[i].ControlID.String() < controls[j].ControlID.String()
	})
}

// buildPortfolioPrompt assembles the deterministic cross-control context block the
// model sees (AC-2). It states the bounded scope honestly ("K of N controls; up
// to M records each"), the deterministic rollup counts (so the model phrases them
// rather than computing them), and each control's bounded cited excerpts. Every
// fact comes straight from the bounded PortfolioSet.
func buildPortfolioPrompt(set PortfolioSet, rollup Rollup) string {
	var b strings.Builder
	scope := "your whole program"
	switch set.Filter.Mode() {
	case "framework":
		scope = "framework " + set.Filter.FrameworkLabel
	case "family":
		scope = "control family " + set.Filter.Family
	}
	fmt.Fprintf(&b, "Portfolio scope: %s.\n", scope)
	fmt.Fprintf(&b,
		"Summarizing the %d most-relevant of %d matched controls; up to %d most-recent CURRENT LIVE records per control.\n",
		rollup.ControlsInSummary, rollup.TotalMatched, MaxRecordsPerControl)
	fmt.Fprintf(&b,
		"Deterministic rollup (state these numbers, do not invent others): %d controls in this summary, %d with current live evidence, %d with no current live evidence, %d evidence records total.\n",
		rollup.ControlsInSummary, rollup.ControlsWithEvidence, rollup.ControlsWithoutEvidence, rollup.TotalRecords)
	b.WriteString("Controls and their evidence:\n")
	for _, c := range set.Controls {
		fmt.Fprintf(&b, "- Control %q (id %s): %d live record(s) on record\n",
			c.ControlTitle, c.ControlID, c.TotalCount)
		for _, e := range c.Records {
			fmt.Fprintf(&b, "    - evidence id %s: kind=%s result=%s observed_at=%s\n",
				e.EvidenceID, e.EvidenceKind, e.Result, e.ObservedAt.UTC().Format(time.RFC3339))
		}
	}
	return b.String()
}
