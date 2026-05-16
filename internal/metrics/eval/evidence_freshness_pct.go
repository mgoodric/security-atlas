package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// evidenceFreshnessPctEvaluator computes the fraction of evidence_freshness
// rows (one per (tenant, control)) that are NOT stale. A tenant with no
// evidence_freshness rows yet returns 0 with dimensions['sample']='empty'.
type evidenceFreshnessPctEvaluator struct{ pool *pgxpool.Pool }

func (e *evidenceFreshnessPctEvaluator) Name() string { return "evidence_freshness_pct" }

func (e *evidenceFreshnessPctEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
		SELECT
			COUNT(*)                                  AS total,
			COUNT(*) FILTER (WHERE is_stale = FALSE)  AS fresh
		FROM evidence_freshness
	`
	var total, fresh int
	if err := e.pool.QueryRow(ctx, query).Scan(&total, &fresh); err != nil {
		return Result{}, fmt.Errorf("evidence_freshness_pct: query: %w", err)
	}
	if total == 0 {
		return Result{Value: 0, Dimensions: map[string]string{"sample": "empty"}}, nil
	}
	return Result{
		Value:      float64(fresh) / float64(total),
		Dimensions: map[string]string{"total_controls": fmt.Sprintf("%d", total)},
	}, nil
}
