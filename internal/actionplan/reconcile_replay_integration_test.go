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

// #3071: the engine does not reconcile plans after a replay at all. The
// control passes live before the plan is linked, so no evaluation has
// reconciled the plan yet; a replay that did reconcile would verify it off
// the live run. Only a live evaluation moves it.
func TestReconcile_ReplayDoesNotReconcileLinkedPlans(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	env.evaluate(t, control, "pass")

	plan := env.completedPlan(t)
	env.link(t, plan, control)
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("after linking a passing control: status %q, want completed (precondition: linking does not reconcile)", got)
	}

	var liveAt time.Time
	if err := env.admin.QueryRow(context.Background(), `
		SELECT max(evaluated_at) FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2 AND evaluated_as_of IS NULL`, env.tenant, control).Scan(&liveAt); err != nil {
		t.Fatalf("read live evaluated_at: %v", err)
	}
	if _, err := env.engine.EvaluateControl(ctxFor(t, env.tenant), control, eval.TriggerReplay, liveAt); err != nil {
		t.Fatalf("replay EvaluateControl: %v", err)
	}
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("after a replay: status %q, want completed (a replay reconciles no plan)", got)
	}
	if moves := env.systemMoves(t, plan); len(moves) != 0 {
		t.Fatalf("system moves after a replay: %v, want none", moves)
	}

	if _, err := env.engine.EvaluateControl(ctxFor(t, env.tenant), control, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("live EvaluateControl: %v", err)
	}
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("after a live evaluation: status %q, want verified", got)
	}
}
