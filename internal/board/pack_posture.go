// pack_posture.go — per-framework posture for the quarterly board pack
// (#2056).
//
// The pack's posture section reads the SAME per-framework posture the
// dashboard shows: the `FrameworkPosture` query (internal/db/queries/
// dashboard.sql) behind GET /v1/frameworks/posture. That query attributes
// coverage per framework version through the SCF anchor spine, drops
// requirements the activated framework scope excludes, and reconstructs a
// 90-day trend from the control_evaluations ledger. The pack does not keep a
// formula of its own: one definition of posture, wherever the board or the
// operator reads it.
//
// A framework version with no activated framework scope in the tenant shows
// "Scope not set" instead of a percentage (#2071 decision B3), because no
// requirement has been decided in or out of scope yet.
package board

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// PostureTrendWindow is the posture trend window: 90 days, the same window
// the dashboard's GET /v1/frameworks/posture uses (internal/api/dashboard
// trendWindow). A framework's baseline is its coverage at the start of this
// window.
const PostureTrendWindow = 90 * 24 * time.Hour

// PostureScopeNotSet is the posture state for a framework version with no
// activated framework scope in the tenant.
const PostureScopeNotSet = "scope-not-set"

// PackFrameworkPosture is one framework version's posture in the pack's
// posture section. The numbers are the dashboard's, verbatim.
//
// The JSON names of the fields the slice-032 FrameworkPosture shape already
// carried (slug, name, coverage_pct, freshness_pct, trend_arrow, delta,
// state) are unchanged, so packs generated before #2056 still decode.
type PackFrameworkPosture struct {
	// FrameworkID is the framework's UUID.
	FrameworkID string `json:"framework_id,omitempty"`
	// Slug is the framework slug (e.g. "soc2").
	Slug string `json:"slug"`
	// Name is the human framework name (e.g. "SOC 2").
	Name string `json:"name"`
	// Version is the framework version the row is for (e.g. "2017").
	Version string `json:"version,omitempty"`
	// FrameworkVersionID is the framework version's UUID — the key of the
	// framework-scope page a "Scope not set" tile links to.
	FrameworkVersionID string `json:"framework_version_id,omitempty"`
	// ScopeNotSet is true when the tenant has no activated framework scope
	// for this version. The percentages are then not reported.
	ScopeNotSet bool `json:"scope_not_set,omitempty"`
	// CoveragePct is the dashboard's coverage_pct [0,100]: in-scope
	// requirements with a covering control / all in-scope requirements.
	CoveragePct float64 `json:"coverage_pct"`
	// FreshnessPct is the dashboard's freshness_composite [0,100].
	FreshnessPct float64 `json:"freshness_pct"`
	// BaselinePct is the coverage at the start of the 90-day trend:
	// CoveragePct - Delta.
	BaselinePct float64 `json:"baseline_pct"`
	// TrendArrow is "up" | "down" | "flat", from Delta.
	TrendArrow string `json:"trend_arrow"`
	// Delta is the dashboard's trend_delta_90d, in percentage points.
	Delta float64 `json:"delta"`
	// State is "audit-ready" | "in-progress" | "at-risk", judged against
	// this framework's own CoveragePct, or "scope-not-set".
	State string `json:"state"`
}

// frameworkPostureRow is one row of the dashboard posture query joined to
// its framework's slug and name.
type frameworkPostureRow struct {
	FrameworkID    uuid.UUID
	VersionID      uuid.UUID
	Slug           string
	Name           string
	Version        string
	CoveragePct    float64
	FreshnessPct   float64
	TrendDelta90d  float64
	ScopeActivated bool
}

// FrameworkPosture runs the dashboard's per-framework posture query for the
// tenant in ctx, with the trend measured from trendCutoff, and joins each row
// to its framework's slug and name. One transaction, two reads.
func (s *PackStore) FrameworkPosture(ctx context.Context, trendCutoff time.Time) ([]frameworkPostureRow, error) {
	var out []frameworkPostureRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		fws, err := q.ListFrameworksForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list frameworks: %w", err)
		}
		names := make(map[uuid.UUID]dbx.Framework, len(fws))
		for _, f := range fws {
			names[uuid.UUID(f.ID.Bytes)] = f
		}
		rows, err := q.FrameworkPosture(ctx, dbx.FrameworkPostureParams{
			TenantID:    pgUUID(tenantID),
			EvaluatedAt: pgTimestamptz(trendCutoff),
		})
		if err != nil {
			return fmt.Errorf("framework posture: %w", err)
		}
		out = make([]frameworkPostureRow, 0, len(rows))
		for _, r := range rows {
			id := uuid.UUID(r.FrameworkID.Bytes)
			fw := names[id]
			out = append(out, frameworkPostureRow{
				FrameworkID:    id,
				VersionID:      uuid.UUID(r.FrameworkVersionID.Bytes),
				Slug:           fw.Slug,
				Name:           fw.Name,
				Version:        r.FrameworkVersion,
				CoveragePct:    r.CoveragePct,
				FreshnessPct:   r.FreshnessComposite,
				TrendDelta90d:  r.TrendDelta90d,
				ScopeActivated: r.ScopeActivated,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// packPostures maps the dashboard posture rows to the pack's posture rows.
// Pure. A row with no activated scope carries no percentages.
func packPostures(rows []frameworkPostureRow) []PackFrameworkPosture {
	out := make([]PackFrameworkPosture, 0, len(rows))
	for _, r := range rows {
		p := PackFrameworkPosture{
			FrameworkID:        r.FrameworkID.String(),
			FrameworkVersionID: r.VersionID.String(),
			Slug:               r.Slug,
			Name:               r.Name,
			Version:            r.Version,
		}
		if p.Name == "" {
			p.Name = p.FrameworkID
		}
		if !r.ScopeActivated {
			p.ScopeNotSet = true
			p.TrendArrow = TrendFlat
			p.State = PostureScopeNotSet
			out = append(out, p)
			continue
		}
		p.CoveragePct = r.CoveragePct
		p.FreshnessPct = r.FreshnessPct
		p.Delta = r.TrendDelta90d
		p.BaselinePct = round2(r.CoveragePct - r.TrendDelta90d)
		p.TrendArrow = trendFromPoints(r.TrendDelta90d)
		// Truncation is floor for a non-negative percentage, so 89.99 stays
		// below the 90-point audit-ready line.
		p.State = stateFromCoverage(int(r.CoveragePct))
		out = append(out, p)
	}
	return out
}

// coverageTrend summarises the scoped frameworks' posture for the
// coverage_trend section: the unweighted mean of their coverage now and of
// their coverage at the start of the 90-day trend. Frameworks without an
// activated scope are left out. Zero scoped frameworks -> all zeros.
func coverageTrend(postures []PackFrameworkPosture) (coverage, baseline float64, scoped int) {
	var sumNow, sumThen float64
	for _, p := range postures {
		if p.ScopeNotSet {
			continue
		}
		scoped++
		sumNow += p.CoveragePct
		sumThen += p.BaselinePct
	}
	if scoped == 0 {
		return 0, 0, 0
	}
	return round2(sumNow / float64(scoped)), round2(sumThen / float64(scoped)), scoped
}

// trendFromPoints maps a signed percentage-point delta to a trend token.
func trendFromPoints(delta float64) string {
	switch {
	case delta > 0:
		return TrendUp
	case delta < 0:
		return TrendDown
	default:
		return TrendFlat
	}
}

// round2 rounds to two decimals, the precision the posture query returns.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// formatPct renders a percentage to one decimal, as the dashboard tile does
// (coverage_pct.toFixed(1)), dropping a trailing ".0": 41.93 -> "41.9",
// 100 -> "100".
func formatPct(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// signedPts formats a percentage-point delta with an explicit sign, to one
// decimal: "+12.5" / "-3" / "0".
func signedPts(v float64) string {
	s := formatPct(v)
	if math.Round(v*10) > 0 {
		return "+" + s
	}
	if s == "-0" {
		return "0"
	}
	return s
}

// Severity bands on the app's single 1..25 risk scale — the register's
// bands (web/app/(authed)/risks/filters.ts severityBand): High from 15,
// Medium from 8, Low from 1, none below.
func severityBandLabel(severity float64) string {
	switch {
	case severity >= 15:
		return "High"
	case severity >= 8:
		return "Medium"
	case severity >= 1:
		return "Low"
	default:
		return "None"
	}
}
