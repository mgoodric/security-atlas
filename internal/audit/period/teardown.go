package period

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// FrozenNoDeleteTrigger refuses DELETE of a frozen audit period for every
// role, the owner included (#3081, migration 20261313000000). Teardown that
// must remove frozen periods disables it with OwnerDelete.
const FrozenNoDeleteTrigger = "audit_periods_frozen_no_delete_trg"

// OwnerDelete runs `DELETE FROM audit_periods WHERE <where>` with args on
// tx, with FrozenNoDeleteTrigger disabled, and enables the trigger again
// after it. where is a SQL fragment the caller writes, never request input;
// args bind its placeholders. Only the table owner (the migrate role) may
// toggle a trigger, so tx must run as the owner: any other role gets
// insufficient_privilege (42501) and nothing is deleted. This is the one
// owner path that removes frozen periods: the demo teardowns
// (internal/demoseed) and integration-test cleanups
// (dbtest.DeleteAuditPeriods) both use it.
//
// The body runs in its own savepoint, rolled back on any error, server- or
// client-side (an argument pgx cannot encode, say). So a failed call leaves
// tx usable with the trigger enabled and nothing deleted, whether or not
// the caller then rolls back. ALTER TABLE is transactional, so no other
// session ever sees the trigger disabled.
//
// Toggling a trigger takes a SHARE ROW EXCLUSIVE lock on audit_periods
// (measured with pg_locks; TestOwnerDelete_LockLevel pins it). On success
// the savepoint is released and the lock is held until tx commits or rolls
// back, not released when the trigger is enabled again. It blocks every
// other session's INSERT, UPDATE and DELETE on audit_periods, for every
// tenant, while reads go on.
//
// lockTimeout bounds the wait for that lock: when it is positive, the ALTER
// fails with lock_not_available (55P03) after that long instead of queueing
// behind a long transaction, and every later audit_periods write queueing
// behind the ALTER. The caller can retry. A timeout under a millisecond
// rounds up to 1ms, never down to 0ms, which Postgres reads as no timeout.
// The session's own lock_timeout is restored once the lock is held. Zero
// keeps the session's setting.
func OwnerDelete(ctx context.Context, tx pgx.Tx, lockTimeout time.Duration, where string, args ...any) (pgconn.CommandTag, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("period: owner delete savepoint: %w", err)
	}
	tag, err := ownerDelete(ctx, sp, lockTimeout, where, args...)
	if err != nil {
		_ = sp.Rollback(ctx)
		return pgconn.CommandTag{}, err
	}
	if err := sp.Commit(ctx); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("period: owner delete release savepoint: %w", err)
	}
	return tag, nil
}

func ownerDelete(ctx context.Context, tx pgx.Tx, lockTimeout time.Duration, where string, args ...any) (pgconn.CommandTag, error) {
	if lockTimeout > 0 {
		var prev string
		if err := tx.QueryRow(ctx, `SELECT current_setting('lock_timeout')`).Scan(&prev); err != nil {
			return pgconn.CommandTag{}, fmt.Errorf("period: read lock_timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`,
			lockTimeoutSetting(lockTimeout)); err != nil {
			return pgconn.CommandTag{}, fmt.Errorf("period: set lock_timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE audit_periods DISABLE TRIGGER `+FrozenNoDeleteTrigger); err != nil {
			return pgconn.CommandTag{}, err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, prev); err != nil {
			return pgconn.CommandTag{}, fmt.Errorf("period: restore lock_timeout: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `ALTER TABLE audit_periods DISABLE TRIGGER `+FrozenNoDeleteTrigger); err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM audit_periods WHERE `+where, args...)
	if err != nil {
		return tag, err
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE audit_periods ENABLE TRIGGER `+FrozenNoDeleteTrigger); err != nil {
		return pgconn.CommandTag{}, err
	}
	return tag, nil
}

// lockTimeoutSetting formats d, which is positive, as a lock_timeout value
// in whole milliseconds, rounded up so it never reaches 0ms.
func lockTimeoutSetting(d time.Duration) string {
	return fmt.Sprintf("%dms", (d+time.Millisecond-1)/time.Millisecond)
}
