//go:build integration

// #3071: a replay of an earlier instant does not move an action plan. The
// engine reconciles linked plans after every run, replays included, and
// action_plan_linked_controls_pass reads each control's latest live run,
// not a replay row stamped with the clock of the run.

package actionplan_test

import (
	"context"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/eval"
)

func TestReconcile_ReplayOfAnEarlierInstantDoesNotMoveThePlan(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)

	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("after a passing evaluation: status %q, want verified", got)
	}

	// Replay an instant just before the passing record was ingested: the
	// ledger held no evidence then, so the replay does not pass.
	var passIngested time.Time
	if err := env.admin.QueryRow(context.Background(), `
		SELECT max(ingested_at) FROM evidence_records
		WHERE tenant_id = $1 AND control_id = $2 AND result = 'pass'`, env.tenant, control).Scan(&passIngested); err != nil {
		t.Fatalf("read pass ingested_at: %v", err)
	}
	if _, err := env.engine.EvaluateControl(ctxFor(t, env.tenant), control, eval.TriggerReplay, passIngested.Add(-time.Microsecond)); err != nil {
		t.Fatalf("replay EvaluateControl: %v", err)
	}
	var replayResult string
	if err := env.admin.QueryRow(context.Background(), `
		SELECT result::text FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2 AND trigger = 'replay'`, env.tenant, control).Scan(&replayResult); err != nil {
		t.Fatalf("read replay row: %v", err)
	}
	if replayResult == "pass" {
		t.Fatalf("replay result = %q, want anything but pass (precondition)", replayResult)
	}

	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("after a non-passing replay of an earlier instant: status %q, want verified", got)
	}
	if env.controlsFailing(t, plan) {
		t.Fatal("controls_failing true after a replay; the live run passes")
	}
}
