//go:build integration

package scim_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/scim"
)

// lastAdminNotification is the notifications.type the #2273 alert writes.
const lastAdminNotification = "tenant.last_admin_deprovisioned"

// seedSuperAdmin creates an active super-admin in a home tenant of their own
// and returns (home tenant, user id).
func seedSuperAdmin(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	home := seedTenant(t, "scim-2273-home-"+uuid.NewString()[:8])
	id := uuid.New()
	ctx := context.Background()
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		 VALUES ($1, $2, $3, 'Super Admin 2273', 'active')`,
		id, home, "sa-2273-"+id.String()[:8]+"@example.com"); err != nil {
		t.Fatalf("seed super-admin user: %v", err)
	}
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'bootstrap_first_install')`, id); err != nil {
		t.Fatalf("seed super_admins: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM notifications WHERE tenant_id = $1`, home)
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM super_admins WHERE user_id = $1`, id)
	})
	return home, id
}

// seedAdminTenant creates a tenant whose `admins` users are provisioned
// through SCIM and hold the manual admin role.
func seedAdminTenant(t *testing.T, admins int) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	tenant := seedTenant(t, "scim-2273-"+uuid.NewString()[:8])
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = adminPool.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1`, tenant)
		_, _ = adminPool.Exec(ctx,
			`DELETE FROM notifications WHERE type = $1 AND payload->>'tenant_id' = $2`,
			lastAdminNotification, tenant.String())
	})
	store := newProvStore()
	ctx := tenantCtx(t, tenant)
	ids := make([]uuid.UUID, 0, admins)
	for i := 0; i < admins; i++ {
		u, err := store.Provision(ctx, uuid.New(), tenant.String(), scim.ProvisionInput{
			UserName: "admin-" + uuid.NewString()[:8] + "@example.com", DisplayName: "Admin", Active: true,
		})
		if err != nil {
			t.Fatalf("provision admin: %v", err)
		}
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by, origin)
			 VALUES ($1, $2, 'admin', 'admin:test', 'manual')`, tenant, u.ID.String()); err != nil {
			t.Fatalf("grant admin: %v", err)
		}
		ids = append(ids, u.ID)
	}
	return tenant, ids
}

// alertsFor counts the last-admin notifications a recipient holds for a tenant.
func alertsFor(t *testing.T, recipientHome, recipient, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications
		  WHERE tenant_id = $1 AND recipient_user_id = $2 AND type = $3
		    AND payload->>'tenant_id' = $4`,
		recipientHome, recipient.String(), lastAdminNotification, tenant.String()).Scan(&n); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	return n
}

func countAction(t *testing.T, tenant uuid.UUID, action string) int {
	t.Helper()
	n := 0
	for _, a := range auditActions(t, tenant) {
		if a == action {
			n++
		}
	}
	return n
}

// deprovisioners are the three SCIM paths that deactivate a user.
var deprovisioners = map[string]func(context.Context, *scim.Store, uuid.UUID, uuid.UUID) error{
	"patch": func(ctx context.Context, s *scim.Store, tenant, id uuid.UUID) error {
		_, err := s.Patch(ctx, uuid.New(), tenant.String(), id, []scim.PatchOperation{
			{Op: "replace", Path: "active", Value: []byte(`false`)},
		})
		return err
	},
	"replace": func(ctx context.Context, s *scim.Store, tenant, id uuid.UUID) error {
		_, err := s.Replace(ctx, uuid.New(), tenant.String(), id, scim.ReplaceInput{
			UserName: "replaced-" + id.String()[:8] + "@example.com", DisplayName: "Gone", Active: false,
		})
		return err
	},
	"delete": func(ctx context.Context, s *scim.Store, tenant, id uuid.UUID) error {
		return s.Delete(ctx, uuid.New(), tenant.String(), id)
	},
}

// #2273: deprovisioning a tenant's last active admin succeeds, writes a
// tenant_last_admin_deprovisioned audit row and notifies super-admins in
// their own tenant, on every SCIM deprovision path.
func TestDeprovision_LastAdminAlertsSuperAdmins_2273(t *testing.T) {
	requireAdminPool(t)
	home, sa := seedSuperAdmin(t)
	for name, deprovision := range deprovisioners {
		t.Run(name, func(t *testing.T) {
			tenant, admins := seedAdminTenant(t, 1)
			if err := deprovision(tenantCtx(t, tenant), newProvStore(), tenant, admins[0]); err != nil {
				t.Fatalf("deprovision must succeed: %v", err)
			}
			if got := countAction(t, tenant, scim.ActionLastAdminDeprovisioned); got != 1 {
				t.Fatalf("%s audit rows = %d, want 1 (actions %v)", scim.ActionLastAdminDeprovisioned, got, auditActions(t, tenant))
			}
			if got := alertsFor(t, home, sa, tenant); got != 1 {
				t.Fatalf("super-admin alerts = %d, want 1", got)
			}
		})
	}
}

// #2273: a deprovision that leaves another active admin, or that removes a
// non-admin, writes no alert.
func TestDeprovision_NoTransitionNoAlert_2273(t *testing.T) {
	requireAdminPool(t)
	home, sa := seedSuperAdmin(t)

	t.Run("second admin remains", func(t *testing.T) {
		tenant, admins := seedAdminTenant(t, 2)
		if err := deprovisioners["patch"](tenantCtx(t, tenant), newProvStore(), tenant, admins[0]); err != nil {
			t.Fatalf("deprovision: %v", err)
		}
		if got := countAction(t, tenant, scim.ActionLastAdminDeprovisioned); got != 0 {
			t.Fatalf("alert audit rows = %d, want 0", got)
		}
		if got := alertsFor(t, home, sa, tenant); got != 0 {
			t.Fatalf("alerts = %d, want 0", got)
		}
	})

	t.Run("non-admin in a tenant with no admin", func(t *testing.T) {
		tenant, _ := seedAdminTenant(t, 0)
		u, err := newProvStore().Provision(tenantCtx(t, tenant), uuid.New(), tenant.String(), scim.ProvisionInput{
			UserName: "member-" + uuid.NewString()[:8] + "@example.com", DisplayName: "Member", Active: true,
		})
		if err != nil {
			t.Fatalf("provision: %v", err)
		}
		if err := deprovisioners["delete"](tenantCtx(t, tenant), newProvStore(), tenant, u.ID); err != nil {
			t.Fatalf("deprovision: %v", err)
		}
		if got := countAction(t, tenant, scim.ActionLastAdminDeprovisioned); got != 0 {
			t.Fatalf("alert audit rows = %d, want 0 (0 -> 0 is not a transition)", got)
		}
	})
}

// #2273: two concurrent deprovisions of a tenant's last two admins serialise
// on the admin-roster lock, so exactly one of them sees the count reach zero
// and raises the alert.
func TestDeprovision_ConcurrentLastTwoAdminsAlertOnce_2273(t *testing.T) {
	requireAdminPool(t)
	home, sa := seedSuperAdmin(t)
	tenant, admins := seedAdminTenant(t, 2)

	ctx := context.Background()
	holder, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT id FROM tenants WHERE id = $1 FOR NO KEY UPDATE`, tenant); err != nil {
		t.Fatalf("hold roster lock: %v", err)
	}

	errs := make(chan error, 2)
	for _, id := range admins {
		go func(id uuid.UUID) {
			errs <- deprovisioners["patch"](tenantCtx(t, tenant), newProvStore(), tenant, id)
		}(id)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var n int
		if err := appPool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE wait_event_type = 'Lock' AND query ILIKE '%FOR NO KEY UPDATE%'
			    AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("both deprovisions did not queue on the admin-roster lock within 15s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = holder.Rollback(ctx)

	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("deprovision: %v", err)
		}
	}
	if got := countAction(t, tenant, scim.ActionLastAdminDeprovisioned); got != 1 {
		t.Fatalf("alert audit rows = %d, want exactly 1", got)
	}
	if got := alertsFor(t, home, sa, tenant); got != 1 {
		t.Fatalf("alerts = %d, want exactly 1", got)
	}
}

// #2273: the SECURITY DEFINER notifier refuses a tenant other than the
// caller's, and writes nothing while the tenant still has an active admin.
func TestNotifySuperAdmins_Guards_2273(t *testing.T) {
	requireAdminPool(t)
	home, sa := seedSuperAdmin(t)
	tenant, _ := seedAdminTenant(t, 1)
	other, _ := seedAdminTenant(t, 0)

	tx, err := appPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `SELECT set_config('app.current_tenant', $1, true)`, tenant.String()); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var n int
	if err := tx.QueryRow(context.Background(),
		`SELECT notify_super_admins_last_admin_deprovisioned($1, $2)`, tenant, uuid.New()).Scan(&n); err != nil {
		t.Fatalf("notify own tenant: %v", err)
	}
	if n != 0 {
		t.Fatalf("notified %d while the tenant has an active admin, want 0", n)
	}
	if _, err := tx.Exec(context.Background(),
		`SELECT notify_super_admins_last_admin_deprovisioned($1, $2)`, other, uuid.New()); err == nil {
		t.Fatal("notify for another tenant succeeded; want a refusal")
	}
	_ = tx.Rollback(context.Background())
	if got := alertsFor(t, home, sa, other); got != 0 {
		t.Fatalf("alerts for the other tenant = %d, want 0", got)
	}
}
