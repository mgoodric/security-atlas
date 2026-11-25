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

// OwnerDelete runs stmt, a DELETE on audit_periods, on tx with
// FrozenNoDeleteTrigger disabled, and enables the trigger again after it.
// Only the table owner (the migrate role) may toggle a trigger, so tx must
// run as the owner. This is the one owner path that removes frozen periods:
// the demo teardowns (internal/demoseed) and integration-test cleanups
// (dbtest.DeleteAuditPeriods) both use it.
//
// ALTER TABLE is transactional, so no other session sees the trigger
// disabled, and a failed DELETE rolls the disable back with the
// transaction or savepoint. Toggling a trigger takes a SHARE ROW EXCLUSIVE
// lock on audit_periods (measured with pg_locks; TestOwnerDelete_LockLevel
// pins it). The lock is held until tx commits or rolls back, not released
// when the trigger is enabled again. It blocks every other session's
// INSERT, UPDATE and DELETE on audit_periods, for every tenant, while reads
// go on.
//
// lockTimeout bounds the wait for that lock: when it is positive, the ALTER
// fails with lock_not_available (55P03) after that long instead of queueing
// behind a long transaction, and every later audit_periods write queueing
// behind the ALTER. The caller can retry. The session's own lock_timeout is
// restored once the lock is held. Zero keeps the session's setting.
func OwnerDelete(ctx context.Context, tx pgx.Tx, lockTimeout time.Duration, stmt string, args ...any) (pgconn.CommandTag, error) {
	if lockTimeout > 0 {
		var prev string
		if err := tx.QueryRow(ctx, `SELECT current_setting('lock_timeout')`).Scan(&prev); err != nil {
			return pgconn.CommandTag{}, fmt.Errorf("period: read lock_timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`,
			fmt.Sprintf("%dms", lockTimeout.Milliseconds())); err != nil {
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
	tag, err := tx.Exec(ctx, stmt, args...)
	if err != nil {
		return tag, err
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE audit_periods ENABLE TRIGGER `+FrozenNoDeleteTrigger); err != nil {
		return pgconn.CommandTag{}, err
	}
	return tag, nil
}
