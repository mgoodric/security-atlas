package exception

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultIdempotencySweepInterval is how often IdempotencySweeper deletes
// expired Idempotency-Keys (#2714). A key past IdempotencyWindow is
// already ignored by GetExceptionIdempotencyKey, so the cadence only
// bounds how long a dead row sits in the table.
const DefaultIdempotencySweepInterval = time.Hour

// IdempotencySweeper deletes exception_idempotency_keys rows older than
// IdempotencyWindow (#2714). Before it, a key sent once and never reused
// stayed forever.
//
// It follows the control import sweeper's two-pool shape:
//
//   - migratorPool (BYPASSRLS) lists the tenant IDs with expired keys, and
//     nothing else; it never writes.
//   - appPool (NOBYPASSRLS) runs each tenant's delete under
//     tenancy.WithTenant, so the write is RLS-scoped (invariant #6).
//
// Every replica may run it: two concurrent deletes of one row are
// harmless.
type IdempotencySweeper struct {
	migratorPool *pgxpool.Pool
	appPool      *pgxpool.Pool
	logger       *slog.Logger
	now          func() time.Time
}

// NewIdempotencySweeper constructs an IdempotencySweeper. migratorPool
// MUST be the migrator role; appPool MUST be atlas_app.
func NewIdempotencySweeper(migratorPool, appPool *pgxpool.Pool, logger *slog.Logger) *IdempotencySweeper {
	if logger == nil {
		logger = slog.Default()
	}
	return &IdempotencySweeper{migratorPool: migratorPool, appPool: appPool, logger: logger, now: time.Now}
}

// WithNow replaces the clock, for tests.
func (s *IdempotencySweeper) WithNow(now func() time.Time) *IdempotencySweeper {
	s.now = now
	return s
}

// Run sweeps once at start, then every interval until ctx is cancelled.
func (s *IdempotencySweeper) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultIdempotencySweepInterval
	}
	sweep := func() {
		n, err := s.SweepOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("exception idempotency sweeper", "err", err.Error())
		}
		if n > 0 {
			s.logger.Info("exception idempotency keys swept", "rows_deleted", n)
		}
	}
	sweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}

// SweepOnce deletes every key, in every tenant, recorded before
// now - IdempotencyWindow, and returns how many it deleted. A tenant whose
// delete errors is logged and skipped.
func (s *IdempotencySweeper) SweepOnce(ctx context.Context) (int64, error) {
	cutoff := s.now().UTC().Add(-IdempotencyWindow)
	tenants, err := s.expiredTenants(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.deleteExpired(ctx, tenantID, cutoff)
		if err != nil {
			s.logger.Error("exception idempotency sweeper: tenant", "tenant_id", tenantID, "err", err.Error())
			continue
		}
		total += n
	}
	return total, nil
}

// expiredTenants lists the tenants with a key recorded before cutoff. It
// is the sweeper's only cross-tenant read and returns IDs only.
func (s *IdempotencySweeper) expiredTenants(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.migratorPool.Query(ctx, `
		SELECT DISTINCT tenant_id::text
		FROM exception_idempotency_keys
		WHERE created_at <= $1
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("exception: list tenants with expired idempotency keys: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("exception: scan tenant: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// deleteExpired deletes one tenant's keys recorded before cutoff, under
// that tenant's RLS context. The bound matches GetExceptionIdempotencyKey,
// which replays only a key with created_at > now - IdempotencyWindow, so
// the sweep never removes a key that could still replay.
func (s *IdempotencySweeper) deleteExpired(ctx context.Context, tenantID string, cutoff time.Time) (int64, error) {
	tctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	tx, err := s.appPool.Begin(tctx)
	if err != nil {
		return 0, fmt.Errorf("exception: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tctx) }()
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(tctx, `
		DELETE FROM exception_idempotency_keys
		WHERE created_at <= $1
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("exception: delete expired idempotency keys: %w", err)
	}
	if err := tx.Commit(tctx); err != nil {
		return 0, fmt.Errorf("exception: commit: %w", err)
	}
	return tag.RowsAffected(), nil
}
