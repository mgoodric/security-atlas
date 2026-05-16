package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// vendorRiskConcentrationEvaluator scores the top-5 vendors' criticality
// concentration. v1 proxy: rank vendors by criticality (high=3,
// medium=2, low=1), take top-5, sum. A high concentration means risk is
// piled into a few vendors.
//
// The slice doc names "data_sensitivity × access_scope" as the ideal
// formula; v1's vendor schema doesn't carry those columns yet so the
// proxy uses the existing `criticality` enum. Documented in the slice's
// decisions log (D7).
type vendorRiskConcentrationEvaluator struct{ pool *pgxpool.Pool }

func (e *vendorRiskConcentrationEvaluator) Name() string { return "vendor_risk_concentration" }

func (e *vendorRiskConcentrationEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
		WITH ranked AS (
			SELECT
				name,
				CASE criticality
					WHEN 'high'   THEN 3
					WHEN 'medium' THEN 2
					WHEN 'low'    THEN 1
					ELSE 0
				END AS score
			FROM vendors
			ORDER BY score DESC, name ASC
			LIMIT 5
		)
		SELECT
			COALESCE(SUM(score), 0) AS concentration,
			COUNT(*)                AS top_n_count
		FROM ranked
	`
	var concentration float64
	var topN int
	if err := e.pool.QueryRow(ctx, query).Scan(&concentration, &topN); err != nil {
		return Result{}, fmt.Errorf("vendor_risk_concentration: query: %w", err)
	}
	return Result{
		Value: concentration,
		Dimensions: map[string]string{
			"v1_proxy":    "criticality_weighted",
			"top_n_count": fmt.Sprintf("%d", topN),
		},
	}, nil
}
