package staleness

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultRecomputeInterval is the staleness-rollup cadence (P0-439-1 — named
// HONESTLY, never "continuous"). 6 hours: frequent enough that a solo operator
// learns about a freshly-stale control the same business day, infrequent
// enough that the alert is plainly periodic, not real-time. The matching
// human copy is staleness.RecomputeIntervalText ("every 6 hours"). Overridable
// for dev loops via the caller passing a smaller interval to Run.
const DefaultRecomputeInterval = 6 * time.Hour

// Scheduler drives the per-tenant staleness rollup on a fixed cadence,
// reusing the slice-076 metrics/scheduler shape:
//
//   - migratorPool (BYPASSRLS) enumerates tenants once per tick (the only
//     cross-tenant read — it returns tenant IDs only, never notification
//     content).
//   - appPool (NOBYPASSRLS) runs each tenant's rollup under
//     tenancy.WithTenant, so every read + write is RLS-scoped to that tenant.
//     Tenant A's stale evidence can NEVER reach Tenant B's notifications
//     (canvas invariant #6 / threat-model I).
//
// The weekly digest is attempted only on a tick whose wall-clock falls in the
// Monday-09:00-UTC window; the per-control alerts run every tick. Both writes
// are idempotent, so an extra tick inside the digest window never
// double-delivers (AC-5).
type Scheduler struct {
	migratorPool *pgxpool.Pool
	appPool      *pgxpool.Pool
	logger       *slog.Logger
	now          func() time.Time
}

// New constructs a Scheduler. migratorPool MUST be the migrator role
// (BYPASSRLS); appPool MUST be atlas_app (NOBYPASSRLS).
func New(migratorPool, appPool *pgxpool.Pool, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Scheduler{
		migratorPool: migratorPool,
		appPool:      appPool,
		logger:       logger,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// SweepReport aggregates one full sweep across all tenants.
type SweepReport struct {
	TenantsSwept   int
	AlertsWritten  int
	DigestsWritten int
	TenantFailures int
	Weekly         bool
}

// Run executes the cron until ctx is cancelled. Fires a sweep immediately on
// start (so a fresh deploy surfaces staleness without waiting a full interval)
// then on every tick.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultRecomputeInterval
	}
	s.logger.Info("staleness scheduler starting", "interval", interval.String())

	sweep := func() SweepReport {
		rep, err := s.SweepOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("staleness scheduler sweep", "err", err.Error())
		}
		return rep
	}

	// First sweep fires inline so a fresh deploy surfaces staleness without
	// waiting a full interval; subsequent sweeps run on the ticker.
	sweep()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("staleness scheduler stopping")
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}

// SweepOnce runs one cycle: enumerate tenants, run each tenant's rollup under
// its own GUC, tally. The weekly digest is attempted iff `now` is in the
// Monday-09:00-UTC window. Per-tenant try/log/continue — one failing tenant
// does not abort the others.
func (s *Scheduler) SweepOnce(ctx context.Context) (SweepReport, error) {
	now := s.now()
	weekly := isWeeklyDigestWindow(now)
	rep := SweepReport{Weekly: weekly}

	tenantIDs, err := dbx.New(s.migratorPool).ListTenantsForMetricsScheduler(ctx)
	if err != nil {
		return rep, err
	}
	fresh := freshness.NewStore(s.appPool)
	store := NewStore(s.appPool, fresh)

	for _, t := range tenantIDs {
		if !t.Valid {
			continue
		}
		tenantID := uuid.UUID(t.Bytes)
		tctx, err := tenancy.WithTenant(ctx, tenantID.String())
		if err != nil {
			s.logger.Error("staleness scheduler: tenant ctx", "tenant", tenantID, "err", err.Error())
			rep.TenantFailures++
			continue
		}
		tr, err := store.RollupTenant(tctx, weekly)
		if err != nil {
			s.logger.Error("staleness scheduler: tenant rollup", "tenant", tenantID, "err", err.Error())
			rep.TenantFailures++
			continue
		}
		rep.TenantsSwept++
		rep.AlertsWritten += tr.AlertsWritten
		rep.DigestsWritten += tr.DigestsWritten
	}
	s.logger.Info("staleness scheduler sweep complete",
		"tenants", rep.TenantsSwept,
		"alerts", rep.AlertsWritten,
		"digests", rep.DigestsWritten,
		"weekly", rep.Weekly,
		"failures", rep.TenantFailures,
	)
	return rep, nil
}

// isWeeklyDigestWindow reports whether `now` is in the weekly digest trigger
// window: Monday, hour 09 UTC. The window is one hour wide; the digest write
// is idempotent on the ISO-week, so multiple ticks inside the window (e.g. a
// 6-hour cadence lands at most one tick in the hour) never double-deliver.
func isWeeklyDigestWindow(now time.Time) bool {
	u := now.UTC()
	return u.Weekday() == time.Monday && u.Hour() == 9
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
