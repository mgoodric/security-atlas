// exception_change.go — re-evaluate a control when one of its exceptions
// changes state (#2269).
//
// The engine reads active exceptions when it evaluates (#2112), so an
// exception that is activated, revoked or expired shows only at the
// control's next evaluation. The exception package calls the function built
// here once the state change has committed, so the control's state follows
// the exception without waiting for the next scheduled run. It never runs
// inside the exception's write transaction: the evaluation opens its own.
package eval

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// exceptionChangeTimeout bounds one re-evaluation. It matches the manual
// "Evaluate now" bound (#2110).
const exceptionChangeTimeout = 30 * time.Second

// ExceptionChangeReevaluator returns the function the exception store and
// the expiry sweep call after an exception changes state. It evaluates the
// control live (FarFuture) under trigger exception_change, in the given
// tenant, through an app-role Engine so RLS binds the writes. It runs
// synchronously and logs a failure rather than returning it: the state
// change has already committed, and the next scheduled run re-evaluates the
// control anyway. The run carries no actor: control_evaluations records
// triggered_by only on manual runs (#2479).
func ExceptionChangeReevaluator(newEngine engineFactory, logger *slog.Logger) func(ctx context.Context, tenantID, controlID uuid.UUID) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return func(ctx context.Context, tenantID, controlID uuid.UUID) {
		tctx, err := tenancy.WithTenant(ctx, tenantID.String())
		if err != nil {
			logger.Error("exception change: tenant context", "tenant_id", tenantID.String(), "err", err.Error())
			return
		}
		tctx, cancel := context.WithTimeout(tctx, exceptionChangeTimeout)
		defer cancel()
		if _, err := newEngine().EvaluateControl(tctx, controlID, TriggerExceptionChange, FarFuture); err != nil {
			logger.Error("exception change: re-evaluate control",
				"tenant_id", tenantID.String(), "control_id", controlID.String(), "err", err.Error())
		}
	}
}
