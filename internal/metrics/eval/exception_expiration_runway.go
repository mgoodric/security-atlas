package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// exceptionExpirationRunwayEvaluator counts active exceptions whose
// expires_at falls inside the next 30 days. Canvas §6.3's
// "Auto-renewal forbidden" means every one of these is a forced human
// re-decision — a stacked runway predicts the next quarter's
// compliance-pause workload.
type exceptionExpirationRunwayEvaluator struct{ pool *pgxpool.Pool }

func (e *exceptionExpirationRunwayEvaluator) Name() string {
	return "exception_expiration_runway"
}

func (e *exceptionExpirationRunwayEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
		SELECT COUNT(*)
		FROM exceptions
		WHERE status = 'active'
		  AND expires_at >= now()
		  AND expires_at <= now() + INTERVAL '30 days'
	`
	var count int
	if err := e.pool.QueryRow(ctx, query).Scan(&count); err != nil {
		return Result{}, fmt.Errorf("exception_expiration_runway: query: %w", err)
	}
	return Result{
		Value: float64(count),
		Dimensions: map[string]string{
			"window_days": "30",
		},
	}, nil
}
