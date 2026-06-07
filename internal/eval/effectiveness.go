// effectiveness.go — control effectiveness scoring.
//
// AC-6: the effectiveness score is the rolling 30-day pass rate computed
// over the control_evaluations ledger. Slice 020 (risk residual derivation)
// consumes this as `operational_score` in the canvas §6.2
// control_effectiveness math:
//
//	control_effectiveness = weight_design     * design_score
//	                      + weight_operation  * operational_score   <-- this slice
//	                      + weight_coverage   * coverage_score
//
// This slice computes and exposes operational_score; design_score and
// coverage_score are slice 020's concern.
package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// EffectivenessWindow is the rolling window for the operational pass-rate.
// Canvas §6.2 says "evidence pass rate over rolling window"; 30 days is the
// AC-6 spec.
const EffectivenessWindow = 30 * 24 * time.Hour

// Effectiveness is the computed operational score for one control.
type Effectiveness struct {
	ControlID uuid.UUID
	// PassRate is evaluations with result=pass divided by total evaluations
	// in the window, in [0,1]. The canvas §6.2 operational_score.
	PassRate float64
	// PassCount / TotalCount are the raw numerator / denominator so the API
	// can show "12 of 15 evaluations passed" rather than just a float.
	PassCount  int
	TotalCount int
	// WindowStart / WindowEnd bound the rolling window the score covers.
	WindowStart time.Time
	WindowEnd   time.Time
}

// Effectiveness computes the rolling-30-day pass rate for one control from
// the control_evaluations ledger.
//
// The window cutoff (`windowEnd - 30d`) is computed HERE in Go and passed to
// the query as an explicit timestamptz parameter — never as a bare
// single-placeholder SQL expression, which would trip pgx's type inference
// (SQLSTATE 42P08). When the control has zero evaluations in the window,
// PassRate is 0 and TotalCount is 0; callers distinguish "0% effective" from
// "no data" via TotalCount.
func (e *Engine) Effectiveness(ctx context.Context, controlID uuid.UUID) (Effectiveness, error) {
	windowEnd := e.now()
	windowStart := windowEnd.Add(-EffectivenessWindow)

	out := Effectiveness{
		ControlID:   controlID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}
	err := e.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, _ pgx.Tx, tenantID uuid.UUID) error {
		// Confirm the control exists in-tenant first so an unknown id is a
		// clean 404 rather than a silently-empty score.
		if _, err := e.store.loadControl(ctx, q, tenantID, controlID); err != nil {
			return err
		}
		rows, err := q.ListControlEvaluationsForEffectiveness(ctx, dbx.ListControlEvaluationsForEffectivenessParams{
			TenantID:      pgUUID(tenantID),
			ControlID:     pgUUID(controlID),
			EvaluatedAt:   pgTimestamptz(windowStart),
			EvaluatedAt_2: pgTimestamptz(windowEnd),
		})
		if err != nil {
			return fmt.Errorf("list evaluations for effectiveness: %w", err)
		}
		for _, r := range rows {
			out.TotalCount++
			if string(r.Result) == ResultPass {
				out.PassCount++
			}
		}
		if out.TotalCount > 0 {
			out.PassRate = float64(out.PassCount) / float64(out.TotalCount)
		}
		return nil
	})
	if err != nil {
		return Effectiveness{}, err
	}
	return out, nil
}
