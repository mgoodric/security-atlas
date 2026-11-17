// generator.go — assembles a Brief from live program metrics and persists
// it as a frozen, append-only snapshot.
//
// The Generator is a pure READER of upstream state — the slice-016 freshness
// + drift read models and the risks + frameworks tables — plus an APPEND of
// the frozen `board_briefs` row (constitutional invariant 2). It computes
// the structured Brief, renders the templated narrative (NO LLM — narrative.go),
// and writes both into one append-only row.
//
// Posture (#2210): each framework's posture and the program coverage figure
// come from the dashboard's per-framework posture query, read and reduced the
// way the board pack reads them (pack_posture.go), so a board member reading
// both sees one coverage number. The slice-031 tenant-wide figure survives
// only as the evidence freshness rate.
package board

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
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
	startedPassing := len(report.StartedPassing)
	driftSummary := DriftSummary{
		WindowDays:          int(DriftWindow.Hours() / 24),
		Since:               report.SinceDate.UTC().Format("2006-01-02"),
		Through:             report.ThroughDate.UTC().Format("2006-01-02"),
		Delta:               report.Delta,
		FlippedOutCount:     len(report.FlippedToOut),
		StartedPassingCount: &startedPassing,
	}

	// --- freshness: the tenant-wide evidence freshness rate ---
	freshnessRows, err := g.freshness.List(ctx)
	if err != nil {
		return Brief{}, fmt.Errorf("board: freshness list: %w", err)
	}

	// --- posture: the board pack's per-framework posture and its coverage
	//     figure, from the same query and the same 90-day trend (#2210) ---
	postureRows, err := g.store.FrameworkPosture(ctx, generatedAt.Add(-PostureTrendWindow))
	if err != nil {
		return Brief{}, err
	}
	postures := packPostures(postureRows)
	coveragePct, _, scopedCount, _ := coverageTrend(postures)

	// --- risks: top-N aging, ranked by residual severity then age ---
	riskRows, err := g.store.ListRisksAsOf(ctx, generatedAt)
	if err != nil {
		return Brief{}, err
	}
	topRisks := rankTopRisks(riskRows, generatedAt, TopRisksCount)

	// --- exceptions: the deterministic exception-status aggregate (slice 751) ---
	// Read under the same RLS context as every other input; the age arithmetic
	// runs against generatedAt so the brief's clock (overridable in tests) is
	// the single time source for both risk aging and exception aging.
	excAgg, err := g.store.ExceptionAggregate(ctx, generatedAt)
	if err != nil {
		return Brief{}, err
	}

	return Brief{
		PeriodEnd:   periodEnd.Format("2006-01-02"),
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Frameworks:  postures,
		Coverage: &CoverageSummary{
			CoveragePct:              coveragePct,
			ScopedFrameworkCount:     scopedCount,
			EvidenceFreshnessRatePct: evidenceFreshnessRate(freshnessRows),
		},
		Drift:      driftSummary,
		TopRisks:   topRisks,
		Exceptions: exceptionSummary(excAgg, generatedAt),
	}, nil
}

// exceptionSummary projects the raw DB aggregate into the Brief's
// ExceptionSummary, resolving the oldest-active start timestamp into an age in
// days against `now` (slice 751). Pure — the only non-trivial branch is the
// no-active-exception case, where the SQL MIN is NULL (a zero start time) and
// the honest age is 0, NOT the age since the zero time.
//
// A start timestamp in the future (an operator scheduled effective_from ahead
// of the brief date) clamps to 0 rather than reporting a negative age: a
// negative day count in a board narrative reads as a bug, and the number the
// board cares about is "how long has this waiver been standing", which is zero
// before it starts. Mirrors the rankTopRisks age-proxy discipline.
func exceptionSummary(agg exceptionAggRow, now time.Time) ExceptionSummary {
	out := ExceptionSummary{
		ActiveCount:  agg.ActiveCount,
		PastDueCount: agg.PastDueCount,
	}
	if !agg.OldestActiveStartedAt.IsZero() {
		if d := now.Sub(agg.OldestActiveStartedAt); d > 0 {
			out.OldestActiveAgeDays = int(d.Hours() / 24)
		}
	}
	return out
}

// evidenceFreshnessRate is the tenant-wide evidence freshness rate: fresh
// controls / total controls in the slice-016 freshness read model, rounded to
// the nearest integer. Zero controls -> 0%.
//
// Until #2210 the brief also derived a "coverage" from this read model
// (fresh controls / controls with evidence). That figure is gone: coverage is
// the posture query's, and this rate is only ever labelled a freshness rate.
func evidenceFreshnessRate(rows []freshness.ControlFreshness) int {
	fresh := 0
	for _, cf := range rows {
		if !cf.IsStale {
			fresh++
		}
	}
	return roundPct(fresh, len(rows))
}

// roundPct computes round(100 * num / den) with den > 0. den == 0 -> 0.
func roundPct(num, den int) int {
	if den <= 0 {
		return 0
	}
	return int((float64(num)*100.0)/float64(den) + 0.5)
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
// severity is read from the residual_score JSONB by risk.ScoreValue, falling
// back to inherent_score when residual has not been derived yet (a risk with
// no linked controls — slice 020 AC-7 — has residual == inherent anyway).
// FAIR risks carry a dollar residual, so risk.RanksAbove ranks them after
// every 5x5 risk rather than comparing dollars with severities (#2185).
// A risk with neither score readable is Unscored and ranks after every
// scored risk (#2511).
func rankTopRisks(rows []riskRow, now time.Time, n int) []RiskAging {
	aging := make([]RiskAging, 0, len(rows))
	for _, r := range rows {
		severity, scored := risk.ScoreValue(r.ResidualScore)
		if severity == 0 {
			var inherentScored bool
			severity, inherentScored = risk.ScoreValue(r.InherentScore)
			scored = scored || inherentScored
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
			CategoryName:     r.CategoryName,
			Treatment:        r.Treatment,
			Methodology:      string(r.Methodology),
			ResidualSeverity: severity,
			Unscored:         !scored,
			AgeDays:          ageDays,
		})
	}
	sort.SliceStable(aging, func(i, j int) bool {
		a, b := aging[i], aging[j]
		if a.Unscored != b.Unscored {
			return b.Unscored
		}
		if risk.RanksAbove(a.Monetary(), a.ResidualSeverity, b.Monetary(), b.ResidualSeverity) {
			return true
		}
		if risk.RanksAbove(b.Monetary(), b.ResidualSeverity, a.Monetary(), a.ResidualSeverity) {
			return false
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
