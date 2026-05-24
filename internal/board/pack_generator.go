// pack_generator.go — assembles a DRAFT quarterly board pack from live
// program metrics and persists it as a draft `board_packs` row.
//
// The PackGenerator is a pure READER of upstream state — the slice-016
// freshness + drift read models, the frameworks + risks tables, and the
// slice-012 control_evaluations (via the board-pack-owned failing-evaluations
// read) — plus an APPEND of the draft `board_packs` row (constitutional
// invariant 2). It computes the structured Pack, renders the templated
// per-section narrative (NO LLM — pack_narrative.go), and writes both into
// one draft row.
//
// Generated vs operator-entered sections (decisions D3 + D4):
//
//   - posture / top_risks / coverage_trend / open_findings are GENERATED
//     from live read models.
//   - operational_metrics / investment are seeded with EMPTY operator inputs
//     and a templated placeholder narrative — the generator never fabricates
//     phishing rates, patch medians, incident counts, vendor numbers, or
//     spend (CLAUDE.md anti-pattern; decision D3).
//   - asks is seeded with a templated placeholder; the operator authors it.
//
// Posture-attribution scope: reuses the slice-031 program-posture model —
// true per-framework control attribution needs the SCF anchor graph +
// framework-scope intersection, which is heavyweight. v1 reports the
// program's tenant-wide posture numbers listed against each registered
// framework.
package board

import (
	"context"
	"fmt"
	"time"
)

// VendorBurndownReader is the narrow view of the slice-122 high-criticality
// vendor burndown surface the PackGenerator reads from (slice 273). An
// interface (not the concrete vendor.Store) keeps the board package free of
// an internal/vendor import cycle and the generator unit-testable without
// a live DB. The integration wires a tiny adapter over vendor.Store.Burndown
// pinned to criticality=high (slice 273 D2) at httpserver.go.
//
// The contract:
//
//   - ReadHighCriticalityBurndown returns the (total, on-time, past-due)
//     triple of high-criticality vendor reviews as of `asOf`. on-time + past
//     due == total in the contract; the generator does not assume a
//     particular sum but renders both totals honestly.
//   - tenant context is carried on `ctx` (the adapter propagates it through
//     the vendor.Store's tenancy GUC).
type VendorBurndownReader interface {
	ReadHighCriticalityBurndown(ctx context.Context, asOf time.Time) (VendorBurndownReadout, error)
}

// VendorBurndownReadout is the value the VendorBurndownReader returns —
// three scalars from the slice-122 surface, used to populate the new
// `vendor_burndown` board-pack section (slice 273).
//
// "on-time" means a high-criticality vendor whose last review was within
// the configured cadence as of `AsOf`. "Past due" means a vendor whose next
// review is overdue. `Total = OnTime + PastDue` in the slice-122 SQL; the
// generator does not enforce that — if the upstream surface ever introduces
// a "no last review" bucket it surfaces honestly as a delta from the sum.
type VendorBurndownReadout struct {
	Total   int64
	OnTime  int64
	PastDue int64
}

// PackGenerator assembles and persists draft quarterly board packs. It is
// wired with the freshness + drift read-model stores (reused from slice
// 031), the board pack Store (frameworks/risks/findings reads, pack append),
// the slice-273 vendor-burndown reader, and a wall-clock source (overridable
// in tests for determinism).
type PackGenerator struct {
	store     *PackStore
	freshness freshnessLister
	drift     driftReporter
	vendors   VendorBurndownReader
	now       func() time.Time
}

// NewPackGenerator wires a PackGenerator. The freshness + drift arguments are
// the slice-016 read-model stores (the same ones slice 031 uses); the pack
// Store handles the frameworks/risks/findings reads and the append. The
// vendors argument is the slice-273 high-criticality vendor-burndown reader
// (the adapter over vendor.Store.Burndown lives at the httpserver wiring
// layer so the board package stays free of an internal/vendor import).
//
// `vendors` may be nil — when nil the generator seeds the vendor_burndown
// section with zero scalars, which is the honest read for a deployment with
// the vendor module disabled or untouched. The integration wires a real
// reader.
func NewPackGenerator(store *PackStore, freshnessStore freshnessLister, driftStore driftReporter, vendors VendorBurndownReader) *PackGenerator {
	return &PackGenerator{
		store:     store,
		freshness: freshnessStore,
		drift:     driftStore,
		vendors:   vendors,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the wall-clock source — used by tests so generated_at
// and the age computations are deterministic.
func (g *PackGenerator) WithClock(now func() time.Time) *PackGenerator {
	g.now = now
	return g
}

// Generate assembles a DRAFT Pack for `periodEnd` from live program metrics,
// renders the templated per-section narrative, and APPENDS the draft
// `board_packs` row. Returns the stored draft pack.
//
// `periodEnd` is the quarter-end report date (YYYY-MM-DD). It LABELS the pack
// — the posture numbers are computed live at call time (the platform has no
// historical posture store; the immutability comes from the publish freeze,
// not a time-travel query). The findings read IS bounded by period_end
// (decision D4 — failing evaluations as of the quarter-end horizon).
func (g *PackGenerator) Generate(ctx context.Context, periodEnd string) (StoredPack, error) {
	periodEndDate, err := time.Parse("2006-01-02", periodEnd)
	if err != nil {
		return StoredPack{}, fmt.Errorf("%w: period_end %q", ErrPackBadPeriodEnd, periodEnd)
	}
	generatedAt := g.now()

	pack, err := g.assemble(ctx, periodEndDate, generatedAt)
	if err != nil {
		return StoredPack{}, err
	}
	narrativeMd, err := RenderPackNarrative(pack)
	if err != nil {
		return StoredPack{}, fmt.Errorf("board: render pack narrative: %w", err)
	}
	return g.store.Insert(ctx, pack, narrativeMd, generatedAt)
}

// assemble builds the structured draft Pack from the read models — pure
// read, no write. Split out from Generate so the assembly is testable in
// isolation from the append.
func (g *PackGenerator) assemble(ctx context.Context, periodEnd, generatedAt time.Time) (Pack, error) {
	// --- drift: the control-drift summary over the slice-031 window ---
	report, err := g.drift.Report(ctx, DriftWindow)
	if err != nil {
		return Pack{}, fmt.Errorf("board: drift report: %w", err)
	}
	driftDelta := report.Delta

	// --- freshness: program coverage + freshness percentages ---
	freshnessRows, err := g.freshness.List(ctx)
	if err != nil {
		return Pack{}, fmt.Errorf("board: freshness list: %w", err)
	}
	coveragePct, freshnessPct := programPosture(freshnessRows)

	// --- frameworks: one posture row per registered framework ---
	frameworks, err := g.store.ListFrameworks(ctx)
	if err != nil {
		return Pack{}, err
	}
	trendArrow := trendFromDelta(driftDelta)
	postureState := stateFromCoverage(coveragePct)
	postures := make([]FrameworkPosture, 0, len(frameworks))
	for _, fw := range frameworks {
		postures = append(postures, FrameworkPosture{
			Slug:         fw.Slug,
			Name:         fw.Name,
			CoveragePct:  coveragePct,
			FreshnessPct: freshnessPct,
			TrendArrow:   trendArrow,
			Delta:        driftDelta,
			State:        postureState,
		})
	}

	// --- risks: top-N aging, ranked by residual severity then age ---
	riskRows, err := g.store.ListRisksAsOf(ctx, generatedAt)
	if err != nil {
		return Pack{}, err
	}
	topRisks := rankTopRisks(riskRows, generatedAt, TopRisksCount)

	// --- open findings: failing control evaluations as of period_end
	//     (decision D4 — board-pack-owned read, period_end horizon) ---
	findings, err := g.store.ListFailingEvaluations(ctx, periodEnd)
	if err != nil {
		return Pack{}, err
	}

	// --- vendor burndown (slice 273): high-criticality vendor reviews
	//     on-time / past-due as of period_end. Pinned to criticality=high
	//     per slice 273 D2. When the generator is wired without a vendor
	//     reader (g.vendors == nil) the section is seeded with zero
	//     scalars — the honest read for a deployment without the vendor
	//     module attached. ---
	var vendorBD VendorBurndownReadout
	if g.vendors != nil {
		vendorBD, err = g.vendors.ReadHighCriticalityBurndown(ctx, periodEnd)
		if err != nil {
			return Pack{}, fmt.Errorf("board: vendor burndown read: %w", err)
		}
	}

	pack := Pack{
		PeriodEnd:   periodEnd.Format("2006-01-02"),
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Status:      PackStatusDraft,
		Sections:    make(map[string]Section, len(SectionKeys)),
	}

	// Generated sections.
	pack.Sections[SectionPosture] = newSection(SectionPosture, SectionData{
		Frameworks: postures,
	})
	pack.Sections[SectionTopRisks] = newSection(SectionTopRisks, SectionData{
		TopRisks: topRisks,
	})
	// coverage_trend: coverage at quarter end vs an operator baseline
	// (decision D5). The baseline starts at 0 — the operator sets it to the
	// prior-quarter coverage. Delta is recomputed whenever the baseline is
	// edited (see PackStore.UpdateSection).
	pack.Sections[SectionCoverageTrend] = newSection(SectionCoverageTrend, SectionData{
		CoveragePct:         coveragePct,
		BaselineCoveragePct: 0,
		CoverageDelta:       coveragePct,
	})
	pack.Sections[SectionOpenFindings] = newSection(SectionOpenFindings, SectionData{
		Findings:      findings,
		FindingsCount: len(findings),
	})

	// Slice 273: vendor_burndown — GENERATED from the slice-122 surface.
	// The on-time percentage is rounded to the nearest integer for the
	// narrative; the fraction is kept as a float for downstream consumers
	// (chart axis, exports). `Total == 0` keeps the percentage at 0 — the
	// honest read for "no high-criticality vendors registered yet".
	pack.Sections[SectionVendorBurndown] = newSection(SectionVendorBurndown, SectionData{
		VendorBurndownTotal:          vendorBD.Total,
		VendorBurndownOnTime:         vendorBD.OnTime,
		VendorBurndownPastDue:        vendorBD.PastDue,
		VendorBurndownOnTimePct:      vendorOnTimePct(vendorBD.Total, vendorBD.OnTime),
		VendorBurndownOnTimeFraction: vendorOnTimeFraction(vendorBD.Total, vendorBD.OnTime),
	})

	// Operator-entered sections (decision D3) — seeded EMPTY with a
	// templated placeholder narrative. The generator does NOT fabricate
	// operational metrics, vendor numbers, or spend.
	pack.Sections[SectionOperational] = newSection(SectionOperational, SectionData{})
	pack.Sections[SectionInvestment] = newSection(SectionInvestment, SectionData{
		SpendUSD:             0,
		CostPerCoveragePoint: 0,
		// CoverageDelta is mirrored here so the investment narrative can
		// reference it; it is kept in sync with the coverage_trend section
		// on edit (see PackStore.UpdateSection).
		CoverageDelta: coveragePct,
	})
	pack.Sections[SectionAsks] = newSection(SectionAsks, SectionData{})

	// Render each section's templated narrative. Pure text/template, NO LLM.
	for key, sec := range pack.Sections {
		text, err := renderSectionNarrative(sec, pack.PeriodEnd)
		if err != nil {
			return Pack{}, err
		}
		sec.TemplatedText = text
		pack.Sections[key] = sec
	}

	return pack, nil
}

// newSection builds a Section envelope with its title and structured data,
// with empty override text and the approved flag false (every section
// starts unapproved — the operator approves each before publish, decision
// D6). The templated narrative is rendered by the caller after the whole
// section map is built.
func newSection(key string, data SectionData) Section {
	return Section{
		Key:          key,
		Title:        sectionTitles[key],
		OverrideText: "",
		Approved:     false,
		Data:         data,
	}
}

// costPerCoveragePoint computes spend / max(delta, 1) (decision D5). A
// non-positive delta floors the denominator at 1 so the figure stays finite
// and meaningful ("this spend bought at most this much per point"). Zero
// spend yields zero.
func costPerCoveragePoint(spendUSD, coverageDelta int) float64 {
	if spendUSD <= 0 {
		return 0
	}
	denom := coverageDelta
	if denom < 1 {
		denom = 1
	}
	return float64(spendUSD) / float64(denom)
}

// vendorOnTimeFraction computes on-time / total (slice 273). Zero total
// yields zero — the honest read for "no high-criticality vendors registered".
// A non-zero total bounds the fraction in [0.0, 1.0].
func vendorOnTimeFraction(total, onTime int64) float64 {
	if total <= 0 {
		return 0
	}
	if onTime < 0 {
		onTime = 0
	}
	if onTime > total {
		onTime = total
	}
	return float64(onTime) / float64(total)
}

// vendorOnTimePct converts vendorOnTimeFraction to a 0-100 integer percentage,
// half-up rounded — the narrative-friendly form (slice 273).
func vendorOnTimePct(total, onTime int64) int {
	if total <= 0 {
		return 0
	}
	return int((vendorOnTimeFraction(total, onTime) * 100.0) + 0.5)
}
