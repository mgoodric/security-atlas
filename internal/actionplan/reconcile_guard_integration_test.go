//go:build integration

// #2458 — follow-ups from the review of #2109's system moves: the guard
// is bound to a per-call nonce, the trigger is exercised for a role that
// holds the table grant but is not the function's owner, and two edge
// cases are pinned (a person-verified plan that later gains a control, and
// a control whose scope resolves to no cell).
//
// Run with:
//
//	go test -tags=integration ./internal/actionplan/ -run 'Reconcile'

package actionplan_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/actionplan"
)

// TestReconcile_GuardRefusesOtherOwnerFunctions: code running as the
// reconcile function's owner, but not inside that function, cannot make
// the system move. The app role sets the marker GUC and calls another
// SECURITY DEFINER function owned by atlas_migrate (the shape of #2362's
// and #2364's functions); inside it current_user is the owner, and the
// guard must still refuse, because the GUC names no nonce the reconcile
// function wrote.
func TestReconcile_GuardRefusesOtherOwnerFunctions(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)
	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q, want verified", got)
	}

	// A stand-in for any other atlas_migrate-owned SECURITY DEFINER
	// function. Committed, so the app pool can call it; dropped after.
	fn := "test_2458_unverify_" + uuid.New().String()[:8]
	ctx := context.Background()
	if _, err := env.admin.Exec(ctx, `
		CREATE FUNCTION `+fn+`(p_plan UUID, p_tenant UUID) RETURNS void
		LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
			UPDATE action_plans SET status = 'completed' WHERE id = p_plan;
		$$;
		GRANT EXECUTE ON FUNCTION `+fn+`(UUID, UUID) TO atlas_app`); err != nil {
		t.Fatalf("create stand-in function: %v", err)
	}
	t.Cleanup(func() {
		if _, err := env.admin.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+fn+`(UUID, UUID)`); err != nil {
			t.Logf("drop stand-in function: %v", err)
		}
	})

	// The old marker value, and a guessed nonce: neither names a row.
	for _, marker := range []string{"on", uuid.New().String()} {
		err := env.appTx(t, "", func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('atlas.action_plan_system_move', $1, true)`, marker); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `SELECT `+fn+`($1, $2)`, plan, env.tenant)
			return err
		})
		if !isPgCode(err, "23514") {
			t.Errorf("marker %q: verified -> completed through another owner function: %v, want check_violation", marker, err)
		}
	}
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("status %q, want verified", got)
	}

	// The app role can neither write a nonce nor read one.
	err := env.appTx(t, "", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO action_plan_system_move_nonces (nonce) VALUES ($1)`, uuid.New())
		return err
	})
	if !isPgCode(err, "42501") {
		t.Errorf("app role wrote a nonce: %v, want insufficient_privilege", err)
	}
	err = env.appTx(t, "", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT count(*) FROM action_plan_system_move_nonces`)
		return err
	})
	if !isPgCode(err, "42501") {
		t.Errorf("app role read the nonces: %v, want insufficient_privilege", err)
	}

	// The reconcile function leaves no nonce behind.
	var n int
	if err := env.admin.QueryRow(ctx, `SELECT count(*) FROM action_plan_system_move_nonces`).Scan(&n); err != nil {
		t.Fatalf("count nonces: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d nonce rows committed, want 0", n)
	}
}

// TestReconcile_TriggerRefusesAGrantedNonOwner: atlas_service_account is
// refused by the table grant before the trigger runs, so the
// TriggerRefusesUnverifyOutsideTheFunction case for it never reaches the
// trigger. Here it holds UPDATE (granted inside a transaction that rolls
// back) and is BYPASSRLS, so the UPDATE reaches the row, and the trigger
// itself must refuse it.
func TestReconcile_TriggerRefusesAGrantedNonOwner(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	plan := env.completedPlan(t)
	env.link(t, plan, control)
	env.evaluate(t, control, "pass")

	ctx := context.Background()
	tx, err := env.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{
		`GRANT SELECT, UPDATE ON action_plans TO atlas_service_account`,
		`SET LOCAL ROLE atlas_service_account`,
		`SELECT set_config('atlas.action_plan_system_move', 'on', true)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE action_plans SET status = 'completed' WHERE id = $1`, plan)
	if !isPgCode(err, "23514") {
		t.Fatalf("granted non-owner: verified -> completed: %v, want check_violation from the trigger", err)
	}
}

// TestReconcile_PersonVerifiedPlanGainsAControl pins #2458's intended
// behaviour: a plan a person verified with no linked control (#2108)
// becomes subject to the system's moves once a control is linked. Linking
// alone moves nothing; the control's evaluations decide from then on, as
// for any plan with linked controls: a pass keeps it verified with no
// system row, and a fail returns it to completed under the system actor.
func TestReconcile_PersonVerifiedPlanGainsAControl(t *testing.T) {
	env := newReconcileEnv(t)
	plan := env.completedPlan(t)
	if code := env.verify(t, plan, env.apprJWT); code != http.StatusOK {
		t.Fatalf("person verify: %d", code)
	}

	control := env.evaluatedControl(t)
	env.link(t, plan, control)
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("after linking a control: status %q, want verified (linking moves nothing)", got)
	}

	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("after the linked control passes: status %q, want verified", got)
	}
	if moves := env.systemMoves(t, plan); len(moves) != 0 {
		t.Fatalf("a pass on a verified plan moved it: %v", moves)
	}

	env.evaluate(t, control, "fail")
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("after the linked control fails: status %q, want completed", got)
	}

	want := [][2]string{{actionplan.StatusVerified, actionplan.StatusCompleted}}
	got := env.systemMoves(t, plan)
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("system audit moves %v, want %v", got, want)
	}
}

// TestReconcile_ControlWithNoScopeCellStillDecides pins #2458's edge case:
// a control whose scope resolves to no cell does not produce an empty run
// that leaves the previous run in charge. The engine writes one row with a
// NULL scope cell (eval.Engine, AC-1), so the newest evaluation decides.
func TestReconcile_ControlWithNoScopeCellStillDecides(t *testing.T) {
	env := newReconcileEnv(t)
	control := env.evaluatedControl(t)
	if _, err := env.admin.Exec(context.Background(),
		`UPDATE controls SET applicability_expr = '{"op":"not","arg":{"op":"true"}}' WHERE id = $1`, control); err != nil {
		t.Fatalf("empty the control's scope: %v", err)
	}
	plan := env.completedPlan(t)
	env.link(t, plan, control)

	env.evaluate(t, control, "pass")
	if got := env.status(t, plan); got != actionplan.StatusVerified {
		t.Fatalf("pass with no scope cell: status %q, want verified", got)
	}
	env.evaluate(t, control, "fail")
	if got := env.status(t, plan); got != actionplan.StatusCompleted {
		t.Fatalf("fail with no scope cell: status %q, want completed (the new run decides)", got)
	}

	var rows, nullCells int
	if err := env.admin.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE scope_cell_id IS NULL)
		FROM control_evaluations
		WHERE tenant_id = $1 AND control_id = $2
		  AND eval_run_id = (SELECT eval_run_id FROM control_evaluations
		                     WHERE tenant_id = $1 AND control_id = $2
		                     ORDER BY evaluated_at DESC, created_at DESC LIMIT 1)`,
		env.tenant, control).Scan(&rows, &nullCells); err != nil {
		t.Fatalf("latest run: %v", err)
	}
	if rows != 1 || nullCells != 1 {
		t.Fatalf("latest run: %d rows, %d with a NULL cell; want one NULL-cell row", rows, nullCells)
	}
}
