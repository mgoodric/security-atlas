package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// auditReadinessScoreEvaluator computes a composite "are we ready?" score.
// v1 formula (simple, transparent, board-explainable):
//
//	(frameworks_with_open_audit_period / frameworks_in_scope) *
//	    (fresh_controls / all_controls)
//
// = the product of "has the period been opened for every framework" and
// "is the evidence fresh". Either factor missing knocks the score down.
//
// A tenant with zero framework_scopes returns 0 with sample=empty.
//
// Note on canvas §8.4 freezing: this evaluator reads `audit_periods` but
// does NOT filter by frozen_at. The board-pack consumer reads the live
// audit_readiness value alongside the frozen-period snapshot's own
// dedicated read; mixing the two would conflate "ready now" with "was
// ready at frozen_at". Documented in the slice's decisions log (D5).
type auditReadinessScoreEvaluator struct{ pool *pgxpool.Pool }

func (e *auditReadinessScoreEvaluator) Name() string { return "audit_readiness_score" }

func (e *auditReadinessScoreEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
		WITH frameworks AS (
			SELECT DISTINCT framework_id
			FROM framework_scopes
			WHERE state = 'active'
		),
		periods AS (
			SELECT DISTINCT fv.framework_id
			FROM audit_periods ap
			JOIN framework_versions fv ON fv.id = ap.framework_version_id
			WHERE ap.status IN ('open', 'fieldwork')
		),
		freshness AS (
			SELECT
				COUNT(*)                                  AS total,
				COUNT(*) FILTER (WHERE is_stale = FALSE)  AS fresh
			FROM evidence_freshness
		)
		SELECT
			(SELECT COUNT(*) FROM frameworks)                                                    AS frameworks_total,
			(SELECT COUNT(*) FROM frameworks f WHERE EXISTS (SELECT 1 FROM periods p WHERE p.framework_id = f.framework_id)) AS frameworks_with_period,
			(SELECT total   FROM freshness)                                                      AS freshness_total,
			(SELECT fresh   FROM freshness)                                                      AS freshness_fresh
	`
	var fwTotal, fwWithPeriod, frTotal, frFresh int
	if err := e.pool.QueryRow(ctx, query).Scan(&fwTotal, &fwWithPeriod, &frTotal, &frFresh); err != nil {
		return Result{}, fmt.Errorf("audit_readiness_score: query: %w", err)
	}
	if fwTotal == 0 {
		return Result{Value: 0, Dimensions: map[string]string{"sample": "empty"}}, nil
	}
	periodFactor := float64(fwWithPeriod) / float64(fwTotal)
	freshFactor := 0.0
	if frTotal > 0 {
		freshFactor = float64(frFresh) / float64(frTotal)
	}
	return Result{
		Value: periodFactor * freshFactor,
		Dimensions: map[string]string{
			"frameworks_total":       fmt.Sprintf("%d", fwTotal),
			"frameworks_with_period": fmt.Sprintf("%d", fwWithPeriod),
			"freshness_total":        fmt.Sprintf("%d", frTotal),
			"freshness_fresh":        fmt.Sprintf("%d", frFresh),
		},
	}, nil
}
