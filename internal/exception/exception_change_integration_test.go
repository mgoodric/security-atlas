//go:build integration

// #2269: activating, revoking or expiring an exception re-evaluates its
// control once the change commits, under trigger exception_change. The
// control's state follows the exception without waiting for the next
// scheduled run.

package exception_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/exception"
)

// latestExceptionChangeResults returns the results of the control's
// exception_change rows, oldest first.
func exceptionChangeResults(t *testing.T, admin *pgxpool.Pool, ctrl uuid.UUID) []string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT result::text FROM control_evaluations
		WHERE control_id = $1 AND trigger = 'exception_change'
		ORDER BY evaluated_at, created_at`, ctrl)
	if err != nil {
		t.Fatalf("query evaluations: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// waitForResults polls until the control has n exception_change rows. The
// store re-evaluates in its own goroutine, so the row lands shortly after
// the transition returns.
func waitForResults(t *testing.T, admin *pgxpool.Pool, ctrl uuid.UUID, n int) []string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := exceptionChangeResults(t, admin, ctrl)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("exception_change rows = %v, want %d", got, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func cleanupEvaluations(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(),
			`DELETE FROM control_evaluations WHERE tenant_id = $1`, tenant); err != nil {
			t.Logf("cleanup control_evaluations: %v", err)
		}
	})
}

func TestExceptionChange_ActivateAndRevokeReevaluateTheControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	cleanupEvaluations(t, admin, tenant)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app).WithReevaluate(
		eval.ExceptionChangeReevaluator(eval.NewEngineFactory(app), nil))
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, "key_requester"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Approve(ctx, created.ID, approverPerson); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := exceptionChangeResults(t, admin, ctrl); len(got) != 0 {
		t.Fatalf("approve re-evaluated the control: %v (only activate, revoke and expire do)", got)
	}

	if _, err := store.Activate(ctx, created.ID, "key_approver", time.Time{}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got := waitForResults(t, admin, ctrl, 1); got[0] != "excepted" {
		t.Fatalf("after activate: results = %v, want excepted", got)
	}

	if _, err := store.Revoke(ctx, created.ID, "key_approver", "control remediated"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := waitForResults(t, admin, ctrl, 2); got[1] == "excepted" {
		t.Fatalf("after revoke: results = %v, want the control's own result", got)
	}
}

func TestExceptionChange_ExpirySweepReevaluatesTheControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	cleanupEvaluations(t, admin, tenant)
	ctrl := seedControl(t, admin, tenant)
	store := exception.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, _ := store.Create(ctx, validCreate(ctrl, "key_requester"))
	_, _ = store.Approve(ctx, created.ID, approverPerson)
	if _, err := store.Activate(ctx, created.ID, "key_approver", time.Time{}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`UPDATE exceptions SET expires_at = now() - interval '1 hour' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	expirer := exception.NewExpirer(admin, nil).WithReevaluate(
		eval.ExceptionChangeReevaluator(eval.NewEngineFactory(app), nil))
	if _, err := expirer.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	// The sweep re-evaluates synchronously, after its commit.
	got := exceptionChangeResults(t, admin, ctrl)
	if len(got) != 1 || got[0] == "excepted" {
		t.Fatalf("after expiry: exception_change results = %v, want one row with the control's own result", got)
	}
}
