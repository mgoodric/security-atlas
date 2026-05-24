// snapshot.go — typed snapshot value + production PanelSource that
// composes the six panel reads from the existing per-domain stores.
//
// Slice 269 P0-A1 anti-criterion: NO new dashboard panels. The
// snapshot is exactly the six panels the dashboard renders:
//
//	framework_posture  — slice 066 dashboard.Store.FrameworkPosture
//	risks              — slice 019 risk.Store.List
//	                     (treatment=mitigate, sort=residual,age —
//	                     same filter the dashboard `/api/dashboard/risks`
//	                     BFF uses)
//	freshness          — slice 016 freshness.Store.List
//	                     (aggregated by class — the panel's bucket=class
//	                     wire shape)
//	drift              — slice 016 drift.Store.Report (since=7d default)
//	upcoming           — slice 066 dashboard.Store.UpcomingItems
//	activity           — slice 066 dashboard.Store.ActivityFeed
//
// Each call delegates to the upstream store, which opens its own
// short-lived tx + applies the tenant GUC. The snapshot composition
// therefore inherits each store's RLS posture — there is no shared
// transaction across panels by design (panel reads are independent).

package dashboardexport

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/dashboard"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// ===== Typed snapshot =====

// Snapshot is the typed value the encoders consume. Each panel is a
// per-type slice / struct — the JSON encoder emits this verbatim
// (with the canonical key set documented in AC-3), and the CSV /
// XLSX encoders project each panel to a (header, rows) shape.
//
// `SnapshotAt` is the request-time UTC timestamp — when the panels
// were composed. The export is NOT a historical snapshot (P0-A3);
// this field is purely for forensic correlation and consumer
// labelling.
type Snapshot struct {
	SnapshotAt time.Time `json:"snapshot_at"`
	Panels     Panels    `json:"panels"`
}

// Panels groups the six panel payloads. Each field's JSON key is the
// canonical panel name; the CSV file names + XLSX sheet names use
// the same keys (with `.csv` appended for the zip-member name and
// truncated to 31 chars for the XLSX sheet-name limit).
type Panels struct {
	FrameworkPosture []FrameworkPosturePanelRow `json:"framework_posture"`
	Risks            []RiskPanelRow             `json:"risks"`
	Freshness        FreshnessPanel             `json:"freshness"`
	Drift            DriftPanel                 `json:"drift"`
	Upcoming         []UpcomingPanelRow         `json:"upcoming"`
	Activity         []ActivityPanelRow         `json:"activity"`
}

// FrameworkPosturePanelRow mirrors the wire shape of
// `/v1/frameworks/posture`'s rows.
type FrameworkPosturePanelRow struct {
	FrameworkID        string  `json:"framework_id"`
	FrameworkVersion   string  `json:"framework_version"`
	CoveragePct        float64 `json:"coverage_pct"`
	FreshnessComposite float64 `json:"freshness_composite"`
	TrendDelta90d      float64 `json:"trend_delta_90d"`
}

// RiskPanelRow mirrors the relevant subset of `/v1/risks`'s rows
// that the dashboard "top risks aging" panel displays. The panel
// uses `treatment=mitigate&sort=residual,age`; the projection here
// surfaces the fields most useful to a board reader (title +
// category + treatment + residual score + age).
//
// `ResidualScore` is the raw JSONB blob the risk store carries (the
// methodology shapes its layout — NIST 800-30 5x5, FAIR, etc.) —
// emitted as an opaque JSON string so consumers can re-parse it
// against the appropriate methodology schema. `CreatedAt` doubles as
// the "age" the residual,age sort uses.
type RiskPanelRow struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Treatment     string `json:"treatment"`
	Category      string `json:"category"`
	Methodology   string `json:"methodology"`
	ResidualScore string `json:"residual_score,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// FreshnessPanel mirrors `/v1/evidence/freshness?bucket=class`'s
// rollup.
type FreshnessPanel struct {
	Bucket     string                 `json:"bucket"`
	Buckets    []FreshnessClassBucket `json:"buckets"`
	Total      int                    `json:"total"`
	TotalStale int                    `json:"total_stale"`
}

// FreshnessClassBucket is one row of the freshness panel.
type FreshnessClassBucket struct {
	FreshnessClass string `json:"freshness_class"`
	Total          int    `json:"total"`
	Fresh          int    `json:"fresh"`
	Stale          int    `json:"stale"`
}

// DriftPanel mirrors `/v1/controls/drift?since=7d`'s payload.
type DriftPanel struct {
	Since           string     `json:"since"`
	Through         string     `json:"through"`
	Delta           int        `json:"delta"`
	FlippedOutCount int        `json:"flipped_out_count"`
	FlippedOut      []DriftRow `json:"flipped_out"`
}

// DriftRow is one flipped-out-of-passing control.
type DriftRow struct {
	ControlID     string `json:"control_id"`
	LastPassing   string `json:"last_passing"`
	CurrentResult string `json:"current_result"`
}

// UpcomingPanelRow mirrors `/v1/upcoming`'s rows.
type UpcomingPanelRow struct {
	DueDate      string `json:"due_date"`
	Category     string `json:"category"`
	Title        string `json:"title"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

// ActivityPanelRow mirrors `/v1/activity`'s rows. The Summary field
// is rendered as the raw JSON string the activity feed returns
// (passed through verbatim into the export — the JSON encoder
// serialises it as a JSON object inside the parent document; the
// CSV / XLSX encoders render it as a stringified JSON cell).
type ActivityPanelRow struct {
	TS           string `json:"ts"`
	EventType    string `json:"event_type"`
	Actor        string `json:"actor"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Summary      string `json:"summary,omitempty"`
}

// ===== Production PanelSource =====

// defaultDriftWindow is the same default the dashboard's drift panel
// uses (7 days; see internal/api/freshnessdrift/handlers.go
// `defaultDriftWindow`). Pinned here so the export shape matches
// the panel a user sees in the live UI by default.
const defaultDriftWindow = 7 * 24 * time.Hour

// defaultActivityPageSize is the slice 066 default page size for the
// activity feed. We pull ONE page only — the dashboard renders the
// first page and the export mirrors what an operator sees.
const defaultActivityPageSize = 50

// defaultUpcomingPageSize matches the default page size for the
// upcoming rollup. Same "first page only" posture as activity.
const defaultUpcomingPageSize = 50

// defaultPostureTrendWindow is the slice 066 dashboard's trend window
// (90 days; see internal/api/dashboard/handler.go `trendWindow`).
const defaultPostureTrendWindow = 90 * 24 * time.Hour

// LivePanelSource is the production PanelSource that composes the
// six panel reads from the existing per-domain stores. Each store
// runs under its own RLS-gated transaction inside its own call;
// the LivePanelSource is a coordinator, not a transaction holder.
type LivePanelSource struct {
	dashboardStore *dashboard.Store
	riskStore      *risk.Store
	freshnessStore *freshness.Store
	driftStore     *drift.Store
}

// NewLivePanelSource constructs a LivePanelSource. All four stores
// MUST be non-nil for the production code path.
func NewLivePanelSource(
	dashboardStore *dashboard.Store,
	riskStore *risk.Store,
	freshnessStore *freshness.Store,
	driftStore *drift.Store,
) *LivePanelSource {
	return &LivePanelSource{
		dashboardStore: dashboardStore,
		riskStore:      riskStore,
		freshnessStore: freshnessStore,
		driftStore:     driftStore,
	}
}

// Snapshot composes the six panels. Each panel is read sequentially;
// the first failure short-circuits with a wrapped error identifying
// which panel failed. We do NOT parallelise the reads — each
// underlying store opens its own pool connection + tx, and
// parallelising would saturate the pool under a burst of export
// requests without a meaningful latency benefit at v1 dashboard
// volumes.
func (s *LivePanelSource) Snapshot(ctx context.Context) (Snapshot, error) {
	out := Snapshot{
		SnapshotAt: time.Now().UTC(),
	}

	// 1. Framework posture.
	postureRows, err := s.dashboardStore.FrameworkPosture(ctx, pgTimestamptz(time.Now().UTC().Add(-defaultPostureTrendWindow)))
	if err != nil {
		return out, fmt.Errorf("framework_posture panel: %w", err)
	}
	out.Panels.FrameworkPosture = make([]FrameworkPosturePanelRow, len(postureRows))
	for i, p := range postureRows {
		out.Panels.FrameworkPosture[i] = FrameworkPosturePanelRow{
			FrameworkID:        uuidStringFromPgtype(p.FrameworkID),
			FrameworkVersion:   p.FrameworkVersion,
			CoveragePct:        p.CoveragePct,
			FreshnessComposite: p.FreshnessComposite,
			TrendDelta90d:      p.TrendDelta90d,
		}
	}

	// 2. Risks — same filter the dashboard's risks panel uses
	//    (treatment=mitigate, sort=residual,age).
	risks, err := s.riskStore.List(ctx, risk.ListFilter{
		Treatment: dbx.RiskTreatmentMitigate,
		Sort:      risk.SortResidualAge,
	})
	if err != nil {
		return out, fmt.Errorf("risks panel: %w", err)
	}
	out.Panels.Risks = make([]RiskPanelRow, len(risks))
	for i, rk := range risks {
		residual := ""
		if len(rk.ResidualScore) > 0 {
			residual = string(rk.ResidualScore)
		}
		out.Panels.Risks[i] = RiskPanelRow{
			ID:            rk.ID.String(),
			Title:         rk.Title,
			Treatment:     string(rk.Treatment),
			Category:      string(rk.Category),
			Methodology:   string(rk.Methodology),
			ResidualScore: residual,
			CreatedAt:     rk.CreatedAt.UTC().Format(time.RFC3339),
		}
	}

	// 3. Freshness — aggregate by class to mirror the dashboard's
	//    bucket=class wire shape.
	freshRows, err := s.freshnessStore.List(ctx)
	if err != nil {
		return out, fmt.Errorf("freshness panel: %w", err)
	}
	out.Panels.Freshness = aggregateFreshnessByClass(freshRows)

	// 4. Drift — default 7-day window.
	driftReport, err := s.driftStore.Report(ctx, defaultDriftWindow)
	if err != nil {
		return out, fmt.Errorf("drift panel: %w", err)
	}
	flips := make([]DriftRow, len(driftReport.FlippedToOut))
	for i, fr := range driftReport.FlippedToOut {
		flips[i] = DriftRow{
			ControlID:     fr.ControlID.String(),
			LastPassing:   fr.LastPassing.UTC().Format("2006-01-02"),
			CurrentResult: fr.CurrentResult,
		}
	}
	out.Panels.Drift = DriftPanel{
		Since:           driftReport.SinceDate.UTC().Format("2006-01-02"),
		Through:         driftReport.ThroughDate.UTC().Format("2006-01-02"),
		Delta:           driftReport.Delta,
		FlippedOutCount: len(flips),
		FlippedOut:      flips,
	}

	// 5. Upcoming — first page only; category filter empty (all
	//    categories merged).
	upcomingRows, err := s.dashboardStore.UpcomingItemsFirstPage(ctx, "", int32(defaultUpcomingPageSize))
	if err != nil {
		return out, fmt.Errorf("upcoming panel: %w", err)
	}
	out.Panels.Upcoming = make([]UpcomingPanelRow, 0, len(upcomingRows))
	for _, u := range upcomingRows {
		out.Panels.Upcoming = append(out.Panels.Upcoming, UpcomingPanelRow{
			DueDate:      tsStringFromPgtype(u.DueDate),
			Category:     u.Category,
			Title:        anyToString(u.Title),
			ResourceType: u.ResourceType,
			ResourceID:   u.ResourceID,
		})
	}

	// 6. Activity — first page only.
	activityRows, err := s.dashboardStore.ActivityFeedFirstPage(ctx, int32(defaultActivityPageSize))
	if err != nil {
		return out, fmt.Errorf("activity panel: %w", err)
	}
	out.Panels.Activity = make([]ActivityPanelRow, 0, len(activityRows))
	for _, ev := range activityRows {
		summary := ""
		if len(ev.Summary) > 0 {
			summary = string(ev.Summary)
		}
		out.Panels.Activity = append(out.Panels.Activity, ActivityPanelRow{
			TS:           tsStringFromPgtype(ev.Ts),
			EventType:    ev.EventType,
			Actor:        ev.Actor,
			ResourceType: ev.ResourceType,
			ResourceID:   ev.ResourceID,
			Summary:      summary,
		})
	}

	return out, nil
}

// aggregateFreshnessByClass groups the per-control freshness rows
// into the same `bucket=class` rollup the
// `/v1/evidence/freshness` panel returns. Inlined here rather than
// reused from internal/api/freshnessdrift because that handler's
// aggregator is package-private; duplicating ~10 lines is cheaper
// than reorganising the freshnessdrift package for one caller.
func aggregateFreshnessByClass(rows []freshness.ControlFreshness) FreshnessPanel {
	type agg struct{ total, fresh, stale int }
	byClass := make(map[string]*agg)
	order := make([]string, 0)
	totalStale := 0
	for _, cf := range rows {
		key := cf.FreshnessClass
		if key == "" {
			key = "unclassified"
		}
		a, ok := byClass[key]
		if !ok {
			a = &agg{}
			byClass[key] = a
			order = append(order, key)
		}
		a.total++
		if cf.IsStale {
			a.stale++
			totalStale++
		} else {
			a.fresh++
		}
	}
	buckets := make([]FreshnessClassBucket, 0, len(order))
	for _, key := range order {
		a := byClass[key]
		buckets = append(buckets, FreshnessClassBucket{
			FreshnessClass: key,
			Total:          a.total,
			Fresh:          a.fresh,
			Stale:          a.stale,
		})
	}
	return FreshnessPanel{
		Bucket:     "class",
		Buckets:    buckets,
		Total:      len(rows),
		TotalStale: totalStale,
	}
}

// ===== pgx helpers =====

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func uuidStringFromPgtype(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func tsStringFromPgtype(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

func anyToString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}
