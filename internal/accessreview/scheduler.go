package accessreview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultReminderInterval is the production cadence for access-review due
// reminder creation. ATLAS_ACCESS_REVIEW_REMINDER_INTERVAL overrides it for
// dev loops.
const DefaultReminderInterval = 24 * time.Hour

type reminderTenantLister func(context.Context, time.Time) ([]uuid.UUID, error)
type reminderFireFunc func(context.Context, uuid.UUID, time.Time) (int, error)

// ReminderScheduler creates access-review due notifications on a fixed
// cadence. The migrator pool enumerates tenants; the app pool performs each
// tenant's writes under tenancy.WithTenant so RLS remains the isolation
// boundary.
type ReminderScheduler struct {
	migratorPool *pgxpool.Pool
	appPool      *pgxpool.Pool
	logger       *slog.Logger
	now          func() time.Time

	listTenants reminderTenantLister
	fireTenant  reminderFireFunc
}

// NewReminderScheduler constructs the production reminder sweeper.
func NewReminderScheduler(migratorPool, appPool *pgxpool.Pool, logger *slog.Logger) *ReminderScheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	s := &ReminderScheduler{
		migratorPool: migratorPool,
		appPool:      appPool,
		logger:       logger,
		now:          func() time.Time { return time.Now().UTC() },
	}
	s.listTenants = s.listTenantsWithDueCampaigns
	s.fireTenant = s.fireDueRemindersForTenant
	return s
}

// Run executes the reminder sweep immediately, then at the configured
// interval until ctx is cancelled.
func (s *ReminderScheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultReminderInterval
	}
	s.logger.Info("access-review reminder scheduler starting", "interval", interval.String())

	sweep := func() {
		if _, err := s.SweepOnce(ctx, s.now()); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("access-review reminder scheduler sweep", "err", err.Error())
		}
	}
	sweep()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("access-review reminder scheduler stopping")
			return nil
		case <-ticker.C:
			sweep()
		}
	}
}

// ReminderSweepReport tallies one reminder sweep.
type ReminderSweepReport struct {
	TenantsSwept         int
	NotificationsCreated int
	TenantFailures       int
}

// SweepOnce enumerates due-campaign tenants and tries each tenant
// independently. A failed tenant is logged and counted but does not abort the
// rest of the sweep.
func (s *ReminderScheduler) SweepOnce(ctx context.Context, now time.Time) (ReminderSweepReport, error) {
	listTenants := s.listTenants
	if listTenants == nil {
		listTenants = s.listTenantsWithDueCampaigns
	}
	fireTenant := s.fireTenant
	if fireTenant == nil {
		fireTenant = s.fireDueRemindersForTenant
	}

	tenantIDs, err := listTenants(ctx, now.UTC())
	if err != nil {
		return ReminderSweepReport{}, fmt.Errorf("access-review reminders: list tenants: %w", err)
	}

	rep := ReminderSweepReport{}
	for _, tenantID := range tenantIDs {
		tctx, err := tenancy.WithTenant(ctx, tenantID.String())
		if err != nil {
			s.logger.Error("access-review reminders: tenant ctx", "tenant", tenantID, "err", err.Error())
			rep.TenantFailures++
			continue
		}
		created, err := fireTenant(tctx, tenantID, now.UTC())
		if err != nil {
			s.logger.Error("access-review reminders: tenant sweep", "tenant", tenantID, "err", err.Error())
			rep.TenantFailures++
			continue
		}
		rep.TenantsSwept++
		rep.NotificationsCreated += created
	}
	s.logger.Info("access-review reminder sweep complete",
		"tenants", rep.TenantsSwept,
		"notifications", rep.NotificationsCreated,
		"failures", rep.TenantFailures,
	)
	return rep, nil
}

func (s *ReminderScheduler) listTenantsWithDueCampaigns(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	rows, err := s.migratorPool.Query(ctx, `
		SELECT DISTINCT c.tenant_id
		FROM access_review_campaigns c
		WHERE c.tenant_id IS NOT NULL
		  AND c.status = 'active'
		  AND c.due_at <= $1
		  AND EXISTS (
		    SELECT 1
		    FROM access_review_items i
		    WHERE i.tenant_id = c.tenant_id
		      AND i.campaign_id = c.id
		      AND i.status = 'pending'
		  )
		ORDER BY c.tenant_id
	`, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var tenantID uuid.UUID
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		out = append(out, tenantID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ReminderScheduler) fireDueRemindersForTenant(ctx context.Context, _ uuid.UUID, now time.Time) (int, error) {
	return NewStore(s.appPool).FireDueReminders(ctx, now.UTC())
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
