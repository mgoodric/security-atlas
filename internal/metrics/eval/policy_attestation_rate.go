package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// policyAttestationRateEvaluator computes the fraction of currently-
// published policy_versions × in-scope users that have a corresponding
// policy_acknowledgments row in the last 365 days.
//
// v1 simplification: the "in-scope users" denominator is the count of
// distinct user_ids that have acknowledged ANY policy in the last 365
// days (a proxy for the active-user population). A connector-fed
// "required ackers per policy" join is the follow-on.
type policyAttestationRateEvaluator struct{ pool *pgxpool.Pool }

func (e *policyAttestationRateEvaluator) Name() string { return "policy_attestation_rate" }

func (e *policyAttestationRateEvaluator) Compute(ctx context.Context) (Result, error) {
	const query = `
		WITH active_users AS (
			SELECT DISTINCT user_id
			FROM policy_acknowledgments
			WHERE acknowledged_at >= now() - INTERVAL '365 days'
		),
		current_policies AS (
			SELECT id FROM policy_versions WHERE status = 'published'
		),
		needed AS (
			SELECT (SELECT COUNT(*) FROM active_users) * (SELECT COUNT(*) FROM current_policies) AS expected
		),
		actual AS (
			SELECT COUNT(*) AS got
			FROM policy_acknowledgments pa
			WHERE pa.policy_version_id IN (SELECT id FROM current_policies)
			  AND pa.acknowledged_at >= now() - INTERVAL '365 days'
		)
		SELECT (SELECT expected FROM needed), (SELECT got FROM actual)
	`
	var expected, got int
	if err := e.pool.QueryRow(ctx, query).Scan(&expected, &got); err != nil {
		return Result{}, fmt.Errorf("policy_attestation_rate: query: %w", err)
	}
	if expected == 0 {
		return Result{Value: 0, Dimensions: map[string]string{"sample": "empty"}}, nil
	}
	val := float64(got) / float64(expected)
	if val > 1.0 {
		val = 1.0
	}
	return Result{
		Value: val,
		Dimensions: map[string]string{
			"expected_acks": fmt.Sprintf("%d", expected),
			"got_acks":      fmt.Sprintf("%d", got),
		},
	}, nil
}
