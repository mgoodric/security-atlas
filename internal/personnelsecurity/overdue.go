// overdue.go — the OE-661 scheduled sweep that surfaces overdue offboarding
// checklists without manual polling.
//
// Shape mirrors the slice-055 decision-overdue Notifier (internal/decision):
// an in-process tick loop (no external cron — the single-VM self-host
// target), a migrator-role (BYPASSRLS) pool that enumerates ONLY the tenant
// ids with overdue open offboarding checklists, and per-tenant work that runs
// through the app-role Store under that tenant's own GUC so every read and
// write is RLS-scoped (canvas invariant #6).
//
// Recipient resolution (OE-661 decision D4): the tenant's ACTIVE users — the
// staleness-rollup convention (internal/staleness, ListActiveUsersForTenant).
// The v1 persona is the solo security leader, so "the tenant's active users"
// IS the security-owner set; there is no narrower per-tenant security-owner
// setting to consult today.
//
// Idempotency: SurfaceOverdueOffboarding's per-(checklist, recipient) dedup
// probe (the notification row is the marker) makes a re-run of the sweep a
// no-op for already-notified pairs — never a double notification.
//
// Honest-interval discipline (canvas anti-pattern): the sweep is DAILY by
// default (ATLAS_PERSONNEL_OVERDUE_INTERVAL overrides for dev loops), the
// same cadence as the decision-overdue notifier. It is not marketed as
// continuous monitoring.
package personnelsecurity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultOverdueSweepInterval is the overdue-offboarding sweep cadence.
// Daily matches the decision-overdue notifier; an offboarding checklist is
// due 24h after the termination date, so a daily sweep surfaces it at most a
// day late — acceptable for an in-app high-priority notification.
const DefaultOverdueSweepInterval = 24 * time.Hour

// OverdueSweepReport tallies one sweep for observability and tests.
type OverdueSweepReport struct {
	// Tenants is the number of tenants that had at least one overdue open
	// offboarding checklist this sweep.
	Tenants int
	// Recipients is the total number of (tenant, active-user) recipients the
	// sweep surfaced to.
	Recipients int
}

// OverdueNotifier is the scheduled overdue-offboarding sweep.
type OverdueNotifier struct {
	migratorPool *pgxpool.Pool
	store        *Store
	logger       *slog.Logger
}

// NewOverdueNotifier constructs an OverdueNotifier. migratorPool MUST be the
// migrator role (BYPASSRLS) — the tenant enumeration crosses tenants (ids
// only, never checklist content). store must be app-role-backed
// (NOBYPASSRLS) so the per-tenant surfacing is RLS-honest.
func NewOverdueNotifier(migratorPool *pgxpool.Pool, store *Store, logger *slog.Logger) *OverdueNotifier {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &OverdueNotifier{migratorPool: migratorPool, store: store, logger: logger}
}

// Run executes the tick loop until ctx is cancelled. The first sweep fires
// immediately so a fresh deploy does not sit silent for a day.
func (n *OverdueNotifier) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultOverdueSweepInterval
	}
	n.logger.Info("personnel overdue notifier starting", "interval", interval.String())
	if _, err := n.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		n.logger.Error("personnel overdue notifier initial sweep", "err", err.Error())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			n.logger.Info("personnel overdue notifier stopping")
			return nil
		case <-ticker.C:
			if _, err := n.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				n.logger.Error("personnel overdue notifier sweep", "err", err.Error())
			}
		}
	}
}

// SweepOnce runs one sweep: enumerate the tenants with overdue open
// offboarding checklists (migrator pool, ids only), then for each tenant
// surface to every active user under that tenant's own context. Per-tenant
// try/log/continue — one failing tenant never aborts the sweep for the rest
// (the metrics-scheduler AC-13 pattern). Exposed for integration tests.
func (n *OverdueNotifier) SweepOnce(ctx context.Context) (OverdueSweepReport, error) {
	rep := OverdueSweepReport{}
	now := n.store.clock()
	tenants, err := dbx.New(n.migratorPool).ListTenantsWithOverdueOffboardingChecklists(ctx, pgTime(now))
	if err != nil {
		return rep, fmt.Errorf("personnel security: list tenants with overdue offboarding: %w", err)
	}
	for _, t := range tenants {
		select {
		case <-ctx.Done():
			return rep, ctx.Err()
		default:
		}
		if !t.Valid {
			continue
		}
		tenantID := uuid.UUID(t.Bytes)
		recipients, err := n.sweepTenant(ctx, tenantID, now)
		if err != nil {
			n.logger.Error("personnel overdue sweep tenant", "tenant", tenantID, "err", err.Error())
			continue
		}
		rep.Tenants++
		rep.Recipients += recipients
	}
	if rep.Tenants > 0 {
		n.logger.Info("personnel overdue sweep complete", "tenants", rep.Tenants, "recipients", rep.Recipients)
	}
	return rep, nil
}

// sweepTenant surfaces one tenant's overdue offboarding checklists to each of
// the tenant's active users. Everything runs under the tenant's GUC through
// the app-role store: the recipient enumeration can only see THIS tenant's
// users, and SurfaceOverdueOffboarding can only see THIS tenant's checklists
// (threat-model: no cross-tenant roster leakage).
func (n *OverdueNotifier) sweepTenant(ctx context.Context, tenantID uuid.UUID, now time.Time) (int, error) {
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return 0, err
	}
	var users []dbx.ListActiveUsersForTenantRow
	err = n.store.inTx(tctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListActiveUsersForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			return err
		}
		users = rows
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("list active users: %w", err)
	}
	notified := 0
	for _, u := range users {
		if !u.ID.Valid {
			continue
		}
		recipient := uuid.UUID(u.ID.Bytes).String()
		if _, err := n.store.SurfaceOverdueOffboarding(tctx, now, recipient); err != nil {
			return notified, fmt.Errorf("surface overdue offboarding for %s: %w", recipient, err)
		}
		notified++
	}
	return notified, nil
}
