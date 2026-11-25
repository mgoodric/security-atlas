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

	if _, err := period.OwnerDelete(ctx, tx, 2*time.Second,
		`DELETE FROM audit_periods WHERE id = $1`, uuid.New()); err != nil {
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
	_, err = period.OwnerDelete(ctx, tx, 200*time.Millisecond,
		`DELETE FROM audit_periods WHERE id = $1`, uuid.New())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("OwnerDelete under a held lock: want lock_not_available (55P03), got %v", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("OwnerDelete waited %v, want about the 200ms lock_timeout", waited)
	}
}
