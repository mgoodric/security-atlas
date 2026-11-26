//go:build integration

package period_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #3089: toggling period.FrozenNoDeleteTrigger takes SHARE ROW EXCLUSIVE on
// audit_periods, not ACCESS EXCLUSIVE, and OwnerDelete still holds it after
// it enables the trigger again: the lock lasts until the transaction ends.
// The session's lock_timeout comes back as it was.
func TestOwnerDelete_LockLevel(t *testing.T) {
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '7s'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}

	if _, err := period.OwnerDelete(ctx, tx, 2*time.Second, `id = $1`, uuid.New()); err != nil {
		t.Fatalf("OwnerDelete: %v", err)
	}

	rows, err := tx.Query(ctx,
		`SELECT mode FROM pg_locks
		  WHERE locktype = 'relation' AND relation = 'audit_periods'::regclass
		    AND pid = pg_backend_pid() AND granted`)
	if err != nil {
		t.Fatalf("pg_locks: %v", err)
	}
	modes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("pg_locks: %v", err)
	}
	held := map[string]bool{}
	for _, m := range modes {
		held[m] = true
	}
	if !held["ShareRowExclusiveLock"] || held["AccessExclusiveLock"] {
		t.Errorf("audit_periods locks held after OwnerDelete = %v, want ShareRowExclusiveLock and no AccessExclusiveLock", modes)
	}

	var timeout string
	if err := tx.QueryRow(ctx, `SHOW lock_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("show lock_timeout: %v", err)
	}
	if timeout != "7s" {
		t.Errorf("lock_timeout after OwnerDelete = %q, want the session's 7s", timeout)
	}
}

// #3089: with a lockTimeout, OwnerDelete gives up with lock_not_available
// (55P03) when another transaction holds a conflicting lock, instead of
// queueing behind it, and deletes nothing.
func TestOwnerDelete_LockTimeout(t *testing.T) {
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	_, p := openPeriod(t, admin, app)

	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	// ROW EXCLUSIVE is what any INSERT, UPDATE or DELETE on the table takes.
	if _, err := holder.Exec(ctx, `LOCK TABLE audit_periods IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("holder lock: %v", err)
	}

	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	start := time.Now()
	_, err = period.OwnerDelete(ctx, tx, 200*time.Millisecond, `id = $1`, p.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("OwnerDelete under a held lock: want lock_not_available (55P03), got %v", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("OwnerDelete waited %v, want about the 200ms lock_timeout", waited)
	}
	// The savepoint rolled back, so tx is still usable, and the open period
	// it would have deleted is there. Commit tx to prove nothing was pending.
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_periods WHERE id = $1`, p.ID).Scan(&n); err != nil {
		t.Fatalf("tx unusable after the timed-out OwnerDelete: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit after the timed-out OwnerDelete: %v", err)
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release holder: %v", err)
	}
	if periods, _ := periodRows(t, admin, p.ID); n != 1 || periods != 1 {
		t.Fatalf("after the timed-out OwnerDelete: %d rows in tx, %d committed, want the period kept", n, periods)
	}
}

// #3094: only the table owner may disable the trigger, so OwnerDelete as
// atlas_app fails with insufficient_privilege (42501) and deletes nothing,
// even an open period atlas_app could delete directly.
func TestOwnerDelete_RefusedForApp(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant, p := openPeriod(t, admin, app)

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	err = pgx.BeginTxFunc(ctx, app, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return err
		}
		_, err := period.OwnerDelete(ctx, tx, 0, `id = $1`, p.ID)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("OwnerDelete as atlas_app: want insufficient_privilege (42501), got %v", err)
	}
	if periods, _ := periodRows(t, admin, p.ID); periods != 1 {
		t.Fatalf("OwnerDelete as atlas_app deleted the period")
	}
}

// #3094: a client-side failure (an argument pgx cannot encode) never reaches
// the server, so Postgres does not abort the transaction. The savepoint
// rollback still undoes the DISABLE: tx stays usable with the trigger
// enabled, and a frozen period is still refused.
func TestOwnerDelete_ClientErrorLeavesTriggerEnabled(t *testing.T) {
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	_, frozen, _ := frozenPeriod(t, admin, app)
	if _, err := admin.Exec(ctx, `UPDATE populations SET audit_period_id = NULL WHERE audit_period_id = $1`, frozen.ID); err != nil {
		t.Fatalf("detach populations: %v", err)
	}

	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := period.OwnerDelete(ctx, tx, 0, `id = $1`, make(chan int)); err == nil {
		t.Fatal("OwnerDelete with an unencodable argument succeeded")
	}

	var enabled string
	if err := tx.QueryRow(ctx,
		`SELECT tgenabled FROM pg_trigger WHERE tgrelid = 'audit_periods'::regclass AND tgname = $1`,
		period.FrozenNoDeleteTrigger).Scan(&enabled); err != nil {
		t.Fatalf("read trigger state in tx: %v", err)
	}
	if enabled != "O" {
		t.Fatalf("trigger state in tx after a failed OwnerDelete = %q, want O (enabled)", enabled)
	}
	_, err = tx.Exec(ctx, `DELETE FROM audit_periods WHERE id = $1`, frozen.ID)
	wantCheckViolation(t, "plain delete of a frozen period after a failed OwnerDelete", err)
}
