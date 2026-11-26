//go:build integration

// #2175: the overdue-offboarding sweep follows the Personnel security
// switch and each recipient's module access.

package personnelsecurity_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
)

func grantRole(t *testing.T, migrate *pgxpool.Pool, tenant, userID, role string) {
	t.Helper()
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test')`,
		tenant, userID, role); err != nil {
		t.Fatalf("grant role: %v", err)
	}
}

// flagsDown fails every switch read, as the flag store does when its DB
// read fails.
type flagsDown struct{}

func (flagsDown) Get(context.Context, string) (featureflag.Flag, error) {
	return featureflag.Flag{}, featureflag.ErrStoreUnavailable
}

func TestOverdueSweepFollowsPersonnelSwitchAndAccess(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, migrate,
		"notifications",
		"evidence_audit_log",
		"evidence_records",
		"personnel_security_checklist_items",
		"personnel_security_checklists",
		"controls",
		"member_module_access",
		"feature_flag_audit_log",
		"feature_flags",
		"user_roles",
		"users",
	)
	controlID := seedAccessControl(t, migrate, tenant)
	member := seedActiveUser(t, migrate, tenant)
	narrowed := seedActiveUser(t, migrate, tenant)
	grantRole(t, migrate, tenant, member.String(), "grc_engineer")
	grantRole(t, migrate, tenant, narrowed.String(), "grc_engineer")

	ctx := dbtest.WithTenantCtx(t, tenant)
	access := moduleaccess.NewStore(app)
	if err := access.Set(ctx, uuid.MustParse(tenant), narrowed.String(), []string{"policies"}, "test"); err != nil {
		t.Fatalf("narrow access: %v", err)
	}
	flags := featureflag.NewStore(app)
	views := moduleaccess.NewViews(access, flags)

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store := newStore(t, migrate, app, now)
	if _, err := store.HandleWorkerEvent(ctx, worker.Worker{
		SourceHRIS: worker.HRISBambooHR,
		WorkerID:   "bhr-modules-1",
		Status:     worker.StatusTerminated,
		EndDate:    now.Add(-48 * time.Hour),
		WorkEmail:  "modules-leaver@example.test",
	}, "evt-modules-1", controlID); err != nil {
		t.Fatalf("HandleWorkerEvent: %v", err)
	}
	notifier := personnelsecurity.NewOverdueNotifier(migrate, store, nil).WithModules(views)

	// #2548: the flag store is down, so the switch is unknown. The tenant
	// is skipped for this sweep rather than read as the seed default (on).
	down := personnelsecurity.NewOverdueNotifier(migrate, store, nil).
		WithModules(moduleaccess.NewViews(access, flagsDown{}))
	if _, err := down.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce (store down): %v", err)
	}
	if got := countNotifications(t, app, tenant, member.String()); got != 0 {
		t.Fatalf("store down: member got %d notifications; want 0", got)
	}

	// Switched off: nobody is notified.
	if _, err := flags.Set(ctx, "personnel.enabled", false, "test", "#2175"); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if _, err := notifier.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce (off): %v", err)
	}
	for name, id := range map[string]string{"member": member.String(), "narrowed": narrowed.String()} {
		if got := countNotifications(t, app, tenant, id); got != 0 {
			t.Errorf("switched off: %s got %d notifications; want 0", name, got)
		}
	}

	// Switched back on: the member is notified, the member without
	// Personnel security access is not.
	if _, err := flags.Set(ctx, "personnel.enabled", true, "test", "#2175"); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if _, err := notifier.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce (on): %v", err)
	}
	if got := countNotifications(t, app, tenant, member.String()); got != 1 {
		t.Errorf("switched on: member got %d notifications; want 1", got)
	}
	if got := countNotifications(t, app, tenant, narrowed.String()); got != 0 {
		t.Errorf("no Personnel security access: got %d notifications; want 0", got)
	}
}
