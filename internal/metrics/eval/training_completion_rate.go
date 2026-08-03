package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type trainingCompletionRateEvaluator struct{ pool *pgxpool.Pool }

func (e *trainingCompletionRateEvaluator) Name() string { return "training_completion_rate" }

func (e *trainingCompletionRateEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
SELECT COUNT(*)::bigint,
       COUNT(*) FILTER (WHERE completed_at IS NOT NULL)::bigint
FROM security_training_assignments`
	var assigned, completed int64
	if err := e.pool.QueryRow(ctx, query).Scan(&assigned, &completed); err != nil {
		return Result{}, fmt.Errorf("training_completion_rate: query: %w", err)
	}
	if assigned == 0 {
		return Result{Value: 0, Dimensions: map[string]string{"sample": "empty"}}, nil
	}
	return Result{
		Value: float64(completed) / float64(assigned),
		Dimensions: map[string]string{
			"assigned":  fmt.Sprintf("%d", assigned),
			"completed": fmt.Sprintf("%d", completed),
		},
	}, nil
}
