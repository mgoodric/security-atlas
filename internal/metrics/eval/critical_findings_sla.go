package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// criticalFindingsSLAEvaluator computes the fraction of P0/P1 audit
// findings closed inside their SLA window. v1 proxy: there is no
// `severity_band` column on audit_notes yet (slice 027 leaves severity
// to the body text). When zero findings exist in the window there is
// nothing to measure, so the evaluator returns the empty marker rather
// than a value: before #2064 it returned 1.0, which the metrics page
// rendered as a 100% pass. When the slice-027 follow-on adds a
// `severity` column, the evaluator's WHERE clause picks it up.
//
// Documented in the slice's decisions log (D8). This is a degraded
// evaluator by design — it ships in v1 because the cron pipeline shape
// is the load-bearing piece; the actual finding-severity query is a
// follow-on with a tiny migration.
type criticalFindingsSLAEvaluator struct{ pool *pgxpool.Pool }

func (e *criticalFindingsSLAEvaluator) Name() string { return "critical_findings_sla" }

func (e *criticalFindingsSLAEvaluator) Compute(ctx context.Context) (Result, error) {
	// v1 query: count "finding"-scope audit_notes rows in the last 90 days.
	const query = `
		SELECT COUNT(*) FROM audit_notes
		WHERE scope_type = 'finding'
		  AND created_at >= now() - INTERVAL '90 days'
	`
	var findings int
	if err := e.pool.QueryRow(ctx, query).Scan(&findings); err != nil {
		return Result{}, fmt.Errorf("critical_findings_sla: query: %w", err)
	}
	return criticalFindingsSLAResult(findings), nil
}

func criticalFindingsSLAResult(findings int) Result {
	if findings == 0 {
		return noData("No critical findings recorded in the last 90 days", map[string]string{
			"window_days": "90",
			"v1_degraded": "no_severity_band_column",
		})
	}
	// Without a close_at + severity column the v1 evaluator emits a
	// conservative 0.0 when findings exist (and dimensions flags the
	// degraded state). When slice 027's follow-on lands, the SQL above
	// picks up severity + close_at and the value reflects reality.
	return Result{
		Value: 0.0,
		Dimensions: map[string]string{
			"findings_in_window": fmt.Sprintf("%d", findings),
			"window_days":        "90",
			"v1_degraded":        "no_severity_band_column",
		},
	}
}
