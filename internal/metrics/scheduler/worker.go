// Package scheduler drives the slice-076 15-minute metrics-evaluator cron.
// Mirrors the eval / freshnessdrift schedulers (slice 012 / slice 016):
//
//   - A migrator-role pool (BYPASSRLS) enumerates tenants once per tick.
//   - An app-role pool runs each tenant's evaluator under
//     tenancy.WithTenant so the per-tenant SELECTs honor RLS.
//   - Per-evaluator try/log/continue — one failing evaluator does NOT
//     abort the run for the others (slice doc AC-13).
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/metrics/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultInterval is the cron cadence. 15 minutes matches the slice doc.
// ATLAS_METRICS_INTERVAL overrides for dev loops.
const DefaultInterval = 15 * time.Minute

// Scheduler runs the per-tenant × per-evaluator sweep on a fixed cadence.
type Scheduler struct {
	migratorPool *pgxpool.Pool
	appPool      *pgxpool.Pool
	registry     *eval.Registry
	logger       *slog.Logger
}

// New constructs a Scheduler. migratorPool MUST be the migrator role
// (BYPASSRLS) — it enumerates every tenant. appPool MUST be the
// atlas_app role — every evaluator's read query runs through it with
// tenancy.WithTenant applied so RLS is honored.
func New(migratorPool *pgxpool.Pool, appPool *pgxpool.Pool, registry *eval.Registry, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Scheduler{
		migratorPool: migratorPool,
		appPool:      appPool,
		registry:     registry,
		logger:       logger,
	}
}

// Run executes the cron until ctx is cancelled. Fires SweepOnce
// immediately on start (so a fresh deploy doesn't wait the full interval
// for first signal) then on every ticker fire.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultInterval
	}
	s.logger.Info("metrics scheduler starting", "interval", interval.String())

	sweep := func() {
		if _, err := s.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("metrics scheduler sweep", "err", err.Error())
		}
	}
	sweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("metrics scheduler stopping")
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}

// SweepReport tallies one full sweep.
type SweepReport struct {
	TenantsSwept        int
	ObservationsWritten int
	EvaluatorFailures   int
}

// SweepOnce runs one cycle: enumerate tenants, iterate evaluators, write
// observations. Returns aggregate counts. Exposed for integration tests.
func (s *Scheduler) SweepOnce(ctx context.Context) (SweepReport, error) {
	rows, err := dbx.New(s.migratorPool).ListTenantsForMetricsScheduler(ctx)
	if err != nil {
		return SweepReport{}, fmt.Errorf("metrics scheduler: list tenants: %w", err)
	}
	rep := SweepReport{}
	for _, row := range rows {
		if !row.Valid {
			continue
		}
		tenantID := uuid.UUID(row.Bytes)
		tctx, err := tenancy.WithTenant(ctx, tenantID.String())
		if err != nil {
			s.logger.Error("metrics scheduler: tenant ctx", "tenant", tenantID, "err", err.Error())
			continue
		}
		// One transaction per tenant: applies the tenant GUC then runs
		// every evaluator's query through the same connection. The
		// commit at the end is for the observation writes.
		if err := s.sweepTenant(tctx, tenantID, &rep); err != nil {
			s.logger.Error("metrics scheduler: tenant sweep", "tenant", tenantID, "err", err.Error())
			continue
		}
		rep.TenantsSwept++
	}
	s.logger.Info("metrics scheduler sweep complete",
		"tenants", rep.TenantsSwept,
		"observations", rep.ObservationsWritten,
		"failures", rep.EvaluatorFailures,
	)
	return rep, nil
}

func (s *Scheduler) sweepTenant(ctx context.Context, tenantID uuid.UUID, rep *SweepReport) error {
	tx, err := s.appPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("apply tenant: %w", err)
	}
	q := dbx.New(tx)

	// Per-evaluator try/log/continue: one failing evaluator does not
	// abort the others. The transaction commits once at the end so
	// either all observations land or none do for this tenant — keeps
	// the per-tenant observation series internally coherent.
	for _, name := range s.registry.Names() {
		evaluator, _ := s.registry.Get(name)
		// Each evaluator's Compute method reads using its own pool
		// (NewRegistry passed the app pool when registering); the
		// tenancy GUC is applied via SET LOCAL on the transaction's
		// session. Both paths land on the same RLS-bound app role.
		//
		// We pass the tenant-context-applied tx context through so
		// nested SELECTs honor the GUC.
		result, err := evaluator.Compute(ctx)
		if err != nil {
			s.logger.Error("metrics scheduler: evaluator failed",
				"tenant", tenantID, "evaluator", name, "err", err.Error())
			rep.EvaluatorFailures++
			continue
		}
		dims := []byte("{}")
		if len(result.Dimensions) > 0 {
			dims = encodeDimensions(result.Dimensions)
		}
		var numeric pgtype.Numeric
		if err := numeric.Scan(fmt.Sprintf("%g", result.Value)); err != nil {
			s.logger.Error("metrics scheduler: encode value",
				"tenant", tenantID, "evaluator", name, "value", result.Value, "err", err.Error())
			rep.EvaluatorFailures++
			continue
		}
		// Insert through the per-tenant tx so the tenant GUC + RLS
		// policy both apply. The tenant_id passed must equal the GUC;
		// the tenant_write WITH CHECK enforces it.
		var tid pgtype.UUID
		tid.Bytes = tenantID
		tid.Valid = true
		if _, err := q.InsertMetricObservation(ctx, dbx.InsertMetricObservationParams{
			TenantID:     tid,
			MetricID:     name, // catalog id == evaluator name (the iff in metrics_catalog)
			ObservedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			NumericValue: numeric,
			Dimensions:   dims,
			Source:       "evaluator:" + name,
		}); err != nil {
			s.logger.Error("metrics scheduler: insert observation",
				"tenant", tenantID, "evaluator", name, "err", err.Error())
			rep.EvaluatorFailures++
			continue
		}
		rep.ObservationsWritten++
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// encodeDimensions serializes a map[string]string into compact JSON.
// json.Marshal of a map[string]string sorts keys alphabetically by
// default — exactly the determinism we want for stable observation rows.
func encodeDimensions(d map[string]string) []byte {
	body, err := json.Marshal(d)
	if err != nil {
		// json.Marshal of map[string]string cannot fail; the err return
		// exists for general Marshal callers. Fallback to empty object
		// so the DB write still succeeds.
		return []byte("{}")
	}
	return body
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
