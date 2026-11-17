//go:build integration

// #2479 — overlapping evaluations of one control. Single-flight in POST
// /v1/controls/{id}/evaluate is per process, so across replicas a manual,
// a scheduled and an ingest evaluation of the same control can run at
// once. These tests run such evaluations concurrently against real Postgres
// and check the state they leave:
//
//   - the #2109 reconcile moves a plan once, not once per overlapping run:
//     its FOR UPDATE makes a second run wait and then re-read the moved row;
//   - the plan's status always matches the control's latest evaluation, and
//     that evaluation matches the newest evidence;
//   - the system's audit rows form an unbroken chain of real moves.
//
// If they fail, evaluations need a per-(tenant, control) advisory lock. They
// pass without one; docs/audit-log/2479-evaluate-follow-ups-decisions.md D2.
//
// Run with:
//
//	go test -tags=integration ./internal/actionplan/ -run Overlap

package actionplan_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// overlapTriggers is the mix of runs that can meet on one control across
// replicas.
var overlapTriggers = []string{eval.TriggerManual, eval.TriggerScheduled, eval.TriggerIngest, eval.TriggerManual, eval.TriggerScheduled, eval.TriggerManual}

// pushEvidence records one evidence row for the control, as a push would.
// It returns the error rather than failing t: it runs off the test
// goroutine.
func (env reconcileEnv) pushEvidence(control uuid.UUID, result string) error {
	ev := uuid.New()
	if _, err := env.admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, $5, '{}'::jsonb, $6, $7)
	`, ev, env.tenant, control, time.Now().UTC(), result, "hash-2479-"+ev.String()[:8], control.String()); err != nil {
		return fmt.Errorf("seed evidence: %w", err)
	}
	return nil
}

// evaluateConcurrently starts every run at once and waits for all of them.
// The ingest run, when push is set, records that evidence first, as the
// ingest subscriber evaluates only after the push commits.
func (env reconcileEnv) evaluateConcurrently(t *testing.T, control uuid.UUID, push string) {
	t.Helper()
	ctx := ctxFor(t, env.tenant)
	start := make(chan struct{})
	errs := make(chan error, len(overlapTriggers))
	var wg sync.WaitGroup
	for i, trigger := range overlapTriggers {
		wg.Add(1)
		go func(i int, trigger string) {
			defer wg.Done()
			<-start
			if trigger == eval.TriggerIngest && push != "" {
				if err := env.pushEvidence(control, push); err != nil {
					errs <- err
					return
				}
			}
			actor := ""
			if trigger == eval.TriggerManual {
				actor = fmt.Sprintf("overlap-caller-%d", i)
			}
			if _, err := env.engine.EvaluateControlBy(ctx, control, trigger, actor, eval.FarFuture); err != nil {
				errs <- fmt.Errorf("run %d (%s): %w", i, trigger, err)
			}
		}(i, trigger)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// latestRunPasses reads the control's latest run the way
// action_plan_linked_controls_pass does, and reports whether every row of
// it passed.
func (env reconcileEnv) latestRunPasses(t *testing.T, control uuid.UUID) bool {
	t.Helper()
	var passes bool
	if err := env.admin.QueryRow(context.Background(), `
		SELECT bool_and(ce.result = 'pass')
		FROM control_evaluations ce
		WHERE ce.tenant_id = $1 AND ce.control_id = $2
		  AND ce.eval_run_id = (
		      SELECT l.eval_run_id FROM control_evaluations l
		      WHERE l.tenant_id = $1 AND l.control_id = $2
		      ORDER BY l.evaluated_at DESC, l.created_at DESC
		      LIMIT 1)`, env.tenant, control).Scan(&passes); err != nil {
		t.Fatalf("latest run: %v", err)
	}
	return passes
}

// assertMoveChain checks the system's audit rows are real moves (before !=
// after), each starting where the last ended, from completed to the plan's
// current status.
func assertMoveChain(t *testing.T, moves [][2]string, final string) {
	t.Helper()
	at := actionplan.StatusCompleted
	for i, m := range moves {
		if m[0] != at || m[0] == m[1] {
			t.Fatalf("audit move %d is %v after status %q; moves %v", i, m, at, moves)
		}
		at = m[1]
	}
	if at != final {
		t.Fatalf("audit moves end at %q; plan is %q; moves %v", at, final, moves)
	}
}

// TestReconcileOverlap_ConcurrentRunsMoveThePlanOnce: six overlapping
// evaluations over the same passing evidence verify the plan with one
// audit row, not one per run.
func TestReconcileOverlap_ConcurrentRunsMoveThePlanOnce(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)
	if err := env.pushEvidence(control, "pass"); err != nil {
		t.Fatal(err)
	}

	env.evaluateConcurrently(t, control, "")

	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q, want verified", got)
	}
	moves := env.systemMoves(t, plan)
	if len(moves) != 1 {
		t.Fatalf("system audit moves %v; want exactly one completed -> verified", moves)
	}
	assertMoveChain(t, moves, actionplan.StatusVerified)

	var runs int
	if err := env.admin.QueryRow(context.Background(),
		`SELECT count(DISTINCT eval_run_id) FROM control_evaluations WHERE tenant_id = $1 AND control_id = $2`,
		env.tenant, control).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != len(overlapTriggers) {
		t.Fatalf("eval runs = %d; want %d, one per overlapping run", runs, len(overlapTriggers))
	}
}

// TestReconcileOverlap_StateConvergesAsEvidenceArrives: passing evidence
// arrives while five other runs overlap the ingest run, then failing
// evidence does. After each round the latest evaluation reflects the
// evidence (a fail in the window outranks a pass, so the second round
// fails), the plan's status matches it, and the audit chain is unbroken.
// Repeated over fresh controls for more interleavings.
func TestReconcileOverlap_StateConvergesAsEvidenceArrives(t *testing.T) {
	env := newReconcileEnv(t)
	for i := 0; i < 4; i++ {
		control := env.evaluatedControl(t)
		plan := env.completedPlan(t)
		env.link(t, plan, control)

		for _, round := range []struct{ evidence, want string }{
			{"pass", actionplan.StatusVerified},
			{"fail", actionplan.StatusCompleted},
		} {
			env.evaluateConcurrently(t, control, round.evidence)

			if got := env.latestRunPasses(t, control); got != (round.evidence == "pass") {
				t.Fatalf("iteration %d: latest evaluation passes=%v after %s evidence", i, got, round.evidence)
			}
			if got := env.status(t, plan); got != round.want {
				t.Fatalf("iteration %d: status %q after %s evidence; want %q", i, got, round.evidence, round.want)
			}
			assertMoveChain(t, env.systemMoves(t, plan), round.want)
		}
	}
}
