package controlimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Sweeper defaults (#2038). A suggestion run that outlives StaleAfter is
// taken to be dead: its process restarted or crashed before it saved. The
// cutoff is generous because a large batch with LLM rerank can run for
// minutes, and failed is final: a run that finishes after its batch was
// failed is refused (ErrSuggestionsNoLongerPending) and writes nothing.
const (
	DefaultStaleSuggestionsAfter = 30 * time.Minute
	DefaultSweepInterval         = 10 * time.Minute
)

// Sweeper fails import batches whose suggestions have been pending longer
// than a cutoff, so a batch whose background run died with its process does
// not read as pending forever. The operator re-uploads a failed batch.
//
// It follows the staleness scheduler's two-pool shape:
//
//   - migratorPool (BYPASSRLS) lists the tenant IDs with stale batches, and
//     nothing else; it never writes.
//   - appPool (NOBYPASSRLS) runs each tenant's update under
//     tenancy.WithTenant, so the write is RLS-scoped (invariant #6).
//
// Every replica may run it. The update claims batches with FOR UPDATE SKIP
// LOCKED and re-checks the pending state, so two replicas sweeping at once
// fail each batch once and never wait on each other.
type Sweeper struct {
	migratorPool *pgxpool.Pool
	appPool      *pgxpool.Pool
	logger       *slog.Logger
	staleAfter   time.Duration
}

// NewSweeper constructs a Sweeper. migratorPool MUST be the migrator role;
// appPool MUST be atlas_app. A non-positive staleAfter takes the default.
func NewSweeper(migratorPool, appPool *pgxpool.Pool, logger *slog.Logger, staleAfter time.Duration) *Sweeper {
	if logger == nil {
		logger = slog.Default()
	}
	if staleAfter <= 0 {
		staleAfter = DefaultStaleSuggestionsAfter
	}
	return &Sweeper{migratorPool: migratorPool, appPool: appPool, logger: logger, staleAfter: staleAfter}
}

// Run sweeps once at start, which covers batches orphaned by the restart
// that started this process, then every interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	sweep := func() {
		if _, err := s.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("control import sweeper", "err", err.Error())
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

// SweepOnce fails every batch, in every tenant, whose suggestions have been
// pending since before the cutoff, and returns how many it failed. A tenant
// whose update errors is logged and skipped.
func (s *Sweeper) SweepOnce(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().Add(-s.staleAfter)
	tenants, err := s.staleTenants(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		ids, err := s.failStale(ctx, tenantID, cutoff)
		if err != nil {
			s.logger.Error("control import sweeper: tenant", "tenant_id", tenantID, "err", err.Error())
			continue
		}
		for _, id := range ids {
			s.logger.Warn("control import suggestions stuck pending; marked failed",
				"tenant_id", tenantID, "batch_id", id, "stale_after", s.staleAfter.String())
		}
		total += len(ids)
	}
	return total, nil
}

// staleTenants lists the tenants with a batch pending since before cutoff.
// It is the sweeper's only cross-tenant read and returns IDs only.
func (s *Sweeper) staleTenants(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.migratorPool.Query(ctx, `
		SELECT DISTINCT tenant_id::text
		FROM control_import_batches
		WHERE suggestions_status = 'pending' AND created_at < $1
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("controlimport: list tenants with stale suggestions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("controlimport: scan tenant: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// failStale marks one tenant's stale pending batches failed, under that
// tenant's RLS context, and returns their IDs.
func (s *Sweeper) failStale(ctx context.Context, tenantID string, cutoff time.Time) ([]string, error) {
	tctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	tx, err := s.appPool.Begin(tctx)
	if err != nil {
		return nil, fmt.Errorf("controlimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tctx) }()
	if err := tenancy.ApplyTenant(tctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(tctx, `
		UPDATE control_import_batches
		SET suggestions_status = 'failed', updated_at = now()
		WHERE id IN (
			SELECT id FROM control_import_batches
			WHERE suggestions_status = 'pending' AND created_at < $1
			FOR UPDATE SKIP LOCKED
		)
		  AND suggestions_status = 'pending'
		RETURNING id::text
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("controlimport: fail stale batches: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("controlimport: scan batch: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlimport: fail stale batches: %w", err)
	}
	if err := tx.Commit(tctx); err != nil {
		return nil, fmt.Errorf("controlimport: commit: %w", err)
	}
	return ids, nil
}
