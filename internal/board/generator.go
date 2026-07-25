// generator.go — assembles a Brief from live program metrics and persists
// it as a frozen, append-only snapshot.
//
// The Generator is a pure READER of upstream state — the slice-016 freshness
// + drift read models and the risks + frameworks tables — plus an APPEND of
// the frozen `board_briefs` row (constitutional invariant 2). It computes
// the structured Brief, renders the templated narrative (NO LLM — narrative.go),
// and writes both into one append-only row.
//
// Posture-attribution scope (decisions log D3): true per-framework control
// attribution needs the SCF anchor graph + framework-scope intersection,
// which is heavyweight. v1 reports the program's tenant-wide posture numbers
// (coverage / freshness / drift) listed against each registered framework.
// The brief is honest about this — it states the program posture and names
// every framework the program runs against. A future slice does true
// per-framework attribution.
package board

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
)

// freshnessLister is the narrow view of *freshness.Store the Generator needs.
// An interface (not the concrete type) keeps the Generator unit-testable
// without a live DB.
type freshnessLister interface {
	List(ctx context.Context) ([]freshness.ControlFreshness, error)
}

// driftReporter is the narrow view of *drift.Store the Generator needs.
type driftReporter interface {
	Report(ctx context.Context, since time.Duration) (drift.DriftReport, error)
}

// Generator assembles and persists monthly board briefs. It is wired with
// the freshness + drift read-model stores, the board Store (frameworks +
// risks reads, brief append), and a wall-clock source (overridable in tests
// for determinism).
type Generator struct {
	store     *Store
	freshness freshnessLister
	drift     driftReporter
	now       func() time.Time
}

// NewGenerator wires a Generator. The freshness + drift arguments are the
// slice-016 read-model stores; the board Store handles the frameworks/risks
// reads and the append.
func NewGenerator(store *Store, freshnessStore freshnessLister, driftStore driftReporter) *Generator {
	return &Generator{
		store:     store,
		freshness: freshnessStore,
		drift:     driftStore,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the wall-clock source — used by tests so generated_at
// and the age computations are deterministic.
func (g *Generator) WithClock(now func() time.Time) *Generator {
	g.now = now
	return g
}

// Generate assembles a Brief for `periodEnd` from live program metrics,
// renders the templated narrative, and APPENDS the frozen snapshot to
// `board_briefs`. Returns the stored brief.
//
// `periodEnd` is the report date (YYYY-MM-DD). It LABELS the brief — the
// posture numbers are computed live at call time (decisions log D2: the
// platform has no historical posture store; the "pin" is the immutability of
// the stored row, not a time-travel query).
func (g *Generator) Generate(ctx context.Context, periodEnd string) (StoredBrief, error) {
	periodEndDate, err := time.Parse("2006-01-02", periodEnd)
	if err != nil {
		return StoredBrief{}, fmt.Errorf("%w: period_end %q", ErrBadPeriodEnd, periodEnd)
	}
	generatedAt := g.now()

	brief, err := g.assemble(ctx, periodEndDate, generatedAt)
	if err != nil {
		return StoredBrief{}, err
	}
	narrativeMd, err := RenderNarrative(brief)
	if err != nil {
		return StoredBrief{}, fmt.Errorf("board: render narrative: %w", err)
	}
	return g.store.Insert(ctx, brief, narrativeMd, generatedAt)
}

// Assemble builds the structured Brief for `periodEnd` from live program
// metrics WITHOUT persisting it — a pure read of the same read models Generate
// uses (freshness + drift + frameworks + risks), under the caller's RLS
// context. It exists so the slice-440 board-narrative surface can reuse the
// EXACT deterministic brief data path as the ground-truth rollup for its
// numeric-verification gate, without writing a board_briefs row (the narrative
// surface computes a rollup, not a frozen brief). `periodEnd` is the report
// date (YYYY-MM-DD); the posture numbers are computed live at call time (same
// discipline as Generate — the "pin" is the immutability of a STORED brief, not
// a time-travel query, so an unpersisted Assemble is a live snapshot).
func (g *Generator) Assemble(ctx context.Context, periodEnd string) (Brief, error) {
	periodEndDate, err := time.Parse("2006-01-02", periodEnd)
	if err != nil {
		return Brief{}, fmt.Errorf("%w: period_end %q", ErrBadPeriodEnd, periodEnd)
	}
	return g.assemble(ctx, periodEndDate, g.now())
}

// assemble builds the structured Brief from the read models — pure read, no
// write. Split out from Generate so the assembly is testable in isolation
// from the append.
func (g *Generator) assemble(ctx context.Context, periodEnd, generatedAt time.Time) (Brief, error) {
	// --- drift: the 30-day control-drift summary ---
	report, err := g.drift.Report(ctx, DriftWindow)
	if err != nil {
		return Brief{}, fmt.Errorf("board: drift report: %w", err)
	}
	driftSummary := DriftSummary{
		WindowDays:      int(DriftWindow.Hours() / 24),
		Since:           report.SinceDate.UTC().Format("2006-01-02"),
		Through:         report.ThroughDate.UTC().Format("2006-01-02"),
		Delta:           report.Delta,
		FlippedOutCount: len(report.FlippedToOut),
	}

	// --- freshness: program coverage + freshness percentages ---
	freshnessRows, err := g.freshness.List(ctx)
	if err != nil {
		return Brief{}, fmt.Errorf("board: freshness list: %w", err)
	}
	coveragePct, freshnessPct := programPosture(freshnessRows)

	// --- frameworks: one posture row per registered framework ---
	frameworks, err := g.store.ListFrameworks(ctx)
	if err != nil {
		return Brief{}, err
	}
	trendArrow := trendFromDelta(driftSummary.Delta)
	postureState := stateFromCoverage(coveragePct)
	postures := make([]FrameworkPosture, 0, len(frameworks))
	for _, fw := range frameworks {
		postures = append(postures, FrameworkPosture{
			Slug:         fw.Slug,
			Name:         fw.Name,
			CoveragePct:  coveragePct,
			FreshnessPct: freshnessPct,
			TrendArrow:   trendArrow,
			Delta:        driftSummary.Delta,
			State:        postureState,
		})
	}

	// --- risks: top-N aging, ranked by residual severity then age ---
	riskRows, err := g.store.ListRisksAsOf(ctx, generatedAt)
	if err != nil {
		return Brief{}, err
	}
	topRisks := rankTopRisks(riskRows, generatedAt, TopRisksCount)

	return Brief{
		PeriodEnd:   periodEnd.Format("2006-01-02"),
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Frameworks:  postures,
		Drift:       driftSummary,
		TopRisks:    topRisks,
	}, nil
}

// programPosture derives the program-wide coverage and freshness percentages
// from the slice-016 freshness read model.
//
//   - freshnessPct = fresh controls / total controls in the read model.
//   - coveragePct  = controls with at least one evidence record that are NOT
//     stale / controls with at least one evidence record. This is the
//     canvas §7.1 control-coverage shape ("active controls with at least one
//     passing evidence record in the freshness window" / "active controls
//     with applicability") approximated over the freshness read model: a
//     fresh control with evidence is "covered". A control with no evidence
//     is neither fresh nor covered.
//
// Both round to the nearest integer. Zero controls -> 0% for both (a program
// with no controls has nothing covered and nothing fresh — the honest read).
func programPosture(rows []freshness.ControlFreshness) (coveragePct, freshnessPct int) {
	if len(rows) == 0 {
		return 0, 0
	}
	total := len(rows)
	fresh := 0
	withEvidence := 0
	coveredWithEvidence := 0
	for _, cf := range rows {
		if !cf.IsStale {
			fresh++
		}
		if cf.EvidenceCount > 0 {
			withEvidence++
			if !cf.IsStale {
				coveredWithEvidence++
			}
		}
	}
	freshnessPct = roundPct(fresh, total)
	if withEvidence == 0 {
		coveragePct = 0
	} else {
		coveragePct = roundPct(coveredWithEvidence, withEvidence)
	}
	return coveragePct, freshnessPct
}

// roundPct computes round(100 * num / den) with den > 0. den == 0 -> 0.
func roundPct(num, den int) int {
	if den <= 0 {
		return 0
	}
	return int((float64(num)*100.0)/float64(den) + 0.5)
}

// trendFromDelta maps the signed 30-day drift delta to a trend-arrow token.
// A positive delta (more controls passing) is an up trend; negative is down;
// zero is flat.
func trendFromDelta(delta int) string {
	switch {
	case delta > 0:
		return TrendUp
	case delta < 0:
		return TrendDown
	default:
		return TrendFlat
	}
}

// stateFromCoverage maps a coverage percentage to the posture label the
// brief surfaces. The thresholds mirror the board-pack mockup's posture
// language (audit-ready / readiness in progress / at-risk).
func stateFromCoverage(coveragePct int) string {
	switch {
	case coveragePct >= 90:
		return "audit-ready"
	case coveragePct >= 70:
		return "in-progress"
	default:
		return "at-risk"
	}
}

// rankTopRisks ranks the supplied risks by residual severity DESC, then by
// age DESC (oldest-touched first), and returns the top n as RiskAging rows.
//
// "Age" is the age-since-treatment proxy: now - updated_at (decisions log
// D4 — the risks table has no treatment-applied timestamp). Residual
// severity is extracted from the residual_score JSONB, falling back to
// inherent_score when residual has not been derived yet (a risk with no
// linked controls — slice 020 AC-7 — has residual == inherent anyway).
func rankTopRisks(rows []riskRow, now time.Time, n int) []RiskAging {
	aging := make([]RiskAging, 0, len(rows))
	for _, r := range rows {
		severity := extractSeverity(r.ResidualScore)
		if severity == 0 {
			severity = extractSeverity(r.InherentScore)
		}
		ageDays := 0
		if !r.UpdatedAt.IsZero() {
			d := now.Sub(r.UpdatedAt)
			if d > 0 {
				ageDays = int(d.Hours() / 24)
			}
		}
		aging = append(aging, RiskAging{
			ID:               r.ID.String(),
			Title:            r.Title,
			Category:         r.Category,
			Treatment:        r.Treatment,
			ResidualSeverity: severity,
			AgeDays:          ageDays,
		})
	}
	sort.SliceStable(aging, func(i, j int) bool {
		if aging[i].ResidualSeverity != aging[j].ResidualSeverity {
			return aging[i].ResidualSeverity > aging[j].ResidualSeverity
		}
		if aging[i].AgeDays != aging[j].AgeDays {
			return aging[i].AgeDays > aging[j].AgeDays
		}
		return aging[i].ID < aging[j].ID
	})
	if len(aging) > n {
		aging = aging[:n]
	}
	return aging
}

// extractSeverity pulls a ranking-comparable severity scalar out of a
// score JSONB blob. The JSONB shape is methodology-dependent (slice 019 /
// 020 / 053):
//
//   - slice 020 residual_score: { "score": <float>, ... }
//   - slice 053 aggregated parents: { "severity": <float>, ... }
//   - slice 019 nist_800_30 / qualitative_5x5 inherent_score:
//     { "likelihood": L, "impact": I } -> severity := L * I
//
// The extraction tries each in turn and returns 0 when none resolves (the
// caller then falls back to the other blob, and ultimately to 0 — a risk
// with no derivable severity ranks last, which is the honest behavior).
func extractSeverity(blob []byte) float64 {
	if len(blob) == 0 {
		return 0
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(blob, &m); err != nil {
		return 0
	}
	if v, ok := numField(m, "score"); ok {
		return v
	}
	if v, ok := numField(m, "severity"); ok {
		return v
	}
	l, lok := numField(m, "likelihood")
	i, iok := numField(m, "impact")
	if lok && iok {
		return l * i
	}
	return 0
}

// numField extracts a numeric value from a raw JSON object field. Returns
// (0, false) when the field is absent or not a number.
func numField(m map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := m[key]
	if !ok {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}
