package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openRiskFinancialExposureEvaluator sums a unit-of-risk magnitude across
// open risks: every risk except an approved acceptance. Untreated risks and
// acceptances pending sign-off stay open (#1746; the predicate mirrors
// risk.IsOpenTreatment). v1 proxy: residual_score's
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
			COUNT(*) AS open_risks_count,
			COUNT(*) FILTER (
				WHERE residual_score->>'likelihood' IS NOT NULL
				  AND residual_score->>'impact' IS NOT NULL
			) AS scored_risks_count
		FROM risks
		WHERE treatment <> 'accept'
		   OR acceptance_status IS DISTINCT FROM 'approved'
	`
	var magnitude float64
	var count, scored int
	if err := e.pool.QueryRow(ctx, query).Scan(&magnitude, &count, &scored); err != nil {
		return Result{}, fmt.Errorf("open_risk_financial_exposure: query: %w", err)
	}
	return openRiskFinancialExposureResult(magnitude, count, scored), nil
}

// openRiskFinancialExposureResult marks the result empty when no open
// risk carries a residual likelihood and impact: the sum would be 0 for
// lack of inputs, not because exposure is nil.
func openRiskFinancialExposureResult(magnitude float64, count, scored int) Result {
	dims := map[string]string{
		"v1_proxy":         "likelihood_times_impact",
		"open_risks_count": fmt.Sprintf("%d", count),
	}
	if scored == 0 {
		return noData("No open risk has a residual score", dims)
	}
	return Result{Value: magnitude, Dimensions: dims}
}
