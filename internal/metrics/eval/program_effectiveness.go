package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// programEffectivenessEvaluator computes the program-effectiveness
// percentage as the fraction of the latest control_evaluations rows
// (one row per (control_id, scope_cell_id)) whose result = 'pass'. The
// "weighted by risk linkage residual" extension lives in a follow-on
// (the cross-table aggregation needs a sql query that doesn't exist
// yet); the v1 numerator/denominator is the direct pass-rate read.
type programEffectivenessEvaluator struct{ pool *pgxpool.Pool }

func (e *programEffectivenessEvaluator) Name() string { return "program_effectiveness" }

func (e *programEffectivenessEvaluator) Compute(ctx context.Context) (Result, error) {
	// DISTINCT ON (control_id, scope_cell_id) picks the LATEST evaluation
	// per (control, scope_cell). Aggregation then counts pass rate over
	// that set. The query honors RLS via the caller's tenant GUC.
	const query = `
		WITH latest AS (
			SELECT DISTINCT ON (control_id, scope_cell_id)
				control_id, scope_cell_id, result
			FROM control_evaluations
			ORDER BY control_id, scope_cell_id, evaluated_at DESC
		)
		SELECT
			COUNT(*)                                    AS total,
			COUNT(*) FILTER (WHERE result = 'pass')     AS passing
		FROM latest
	`
	var total, passing int
	if err := e.pool.QueryRow(ctx, query).Scan(&total, &passing); err != nil {
		return Result{}, fmt.Errorf("program_effectiveness: query: %w", err)
	}
	if total == 0 {
		return Result{Value: 0, Dimensions: map[string]string{"sample": "empty"}}, nil
	}
	return Result{
		Value:      float64(passing) / float64(total),
		Dimensions: map[string]string{"sample": "all_controls"},
	}, nil
}
