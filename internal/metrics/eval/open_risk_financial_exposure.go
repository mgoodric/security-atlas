package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openRiskFinancialExposureEvaluator sums a unit-of-risk magnitude across
// open (treatment != 'accept') risks. v1 proxy: residual_score's
// likelihood × impact product (extracted from the JSONB column), because
// v1's risk register stores residual as {likelihood, impact} not as a
// dollar ALE. Documented in the slice's decisions log (D6).
//
// When a tenant ships FAIR-method risks with an `annualized_loss` field
// in residual_score, a future evaluator slice will swap to `SUM(residual_score->>'annualized_loss')`.
// The catalog metric's `unit: dollars_ale` is forward-looking; the v1
// numeric is the magnitude proxy and the dimensions field labels it.
type openRiskFinancialExposureEvaluator struct{ pool *pgxpool.Pool }

func (e *openRiskFinancialExposureEvaluator) Name() string {
	return "open_risk_financial_exposure"
}

func (e *openRiskFinancialExposureEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
		SELECT
			COALESCE(SUM(
				COALESCE((residual_score->>'likelihood')::numeric, 0) *
				COALESCE((residual_score->>'impact')::numeric, 0)
			), 0) AS magnitude_sum,
			COUNT(*) AS open_risks_count
		FROM risks
		WHERE treatment <> 'accept'
	`
	var magnitude float64
	var count int
	if err := e.pool.QueryRow(ctx, query).Scan(&magnitude, &count); err != nil {
		return Result{}, fmt.Errorf("open_risk_financial_exposure: query: %w", err)
	}
	return Result{
		Value: magnitude,
		Dimensions: map[string]string{
			"v1_proxy":         "likelihood_times_impact",
			"open_risks_count": fmt.Sprintf("%d", count),
		},
	}, nil
}
