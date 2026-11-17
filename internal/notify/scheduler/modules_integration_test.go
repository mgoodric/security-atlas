//go:build integration

package scheduler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/notify/email"
	"github.com/mgoodric/security-atlas/internal/notify/scheduler"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #2175 helpers: a live role, a second unread notification, and cleanup of
// the module tables seedUserWithType does not know about.
func moduleSeed(t *testing.T, admin *pgxpool.Pool, tenantID, userID uuid.UUID, role string, extraType string) {
	t.Helper()
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test')`,
		tenantID, userID.String(), role); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, now())
	`, uuid.New(), tenantID, userID.String(), extraType); err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM member_module_access WHERE tenant_id = $1`,
			`DELETE FROM feature_flags WHERE tenant_id = $1`,
			`DELETE FROM user_roles WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenantID); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
}

func tenantCtx(t *testing.T, tenantID uuid.UUID) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// A recipient without Policies access gets no policy reminders in their
// digest; their other notifications still arrive, and a member with
// access still gets the policy reminder.
func TestSweepOnce_DigestDropsPolicyRemindersWithoutAccess(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	prov := &fakeProvider{}
	access := moduleaccess.NewStore(app)
	views := moduleaccess.NewViews(access, featureflag.NewStore(app))
	ch := email.NewChannel(app, prov, "https://atlas.example.test").WithModules(views)

	tNarrow, uNarrow := seedUserWithType(t, admin, "narrowed@example.test", "policy_ack_due", true)
	moduleSeed(t, admin, tNarrow, uNarrow, "grc_engineer", "evidence.staleness")
	if err := access.Set(tenantCtx(t, tNarrow), tNarrow, uNarrow.String(), []string{"risk"}, "test"); err != nil {
		t.Fatalf("narrow access: %v", err)
	}
	tAll, uAll := seedUserWithType(t, admin, "all@example.test", "policy_ack_due", true)
	moduleSeed(t, admin, tAll, uAll, "grc_engineer", "evidence.staleness")

	s := scheduler.New(admin, []scheduler.Channel{scheduler.EmailChannel(ch)}, nil)
	if _, err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	narrowBody := prov.bodyFor("narrowed@example.test")
	if narrowBody == "" {
		t.Fatal("narrowed member got no digest; the stale-evidence notification should still arrive")
	}
	if strings.Contains(narrowBody, "Policy acknowledgments") {
		t.Errorf("no Policies access: digest counts policy reminders:\n%s", narrowBody)
	}
	if !strings.Contains(narrowBody, "Stale-evidence") {
		t.Errorf("no Policies access: digest lost the stale-evidence count:\n%s", narrowBody)
	}
	if body := prov.bodyFor("all@example.test"); !strings.Contains(body, "Policy acknowledgments") {
		t.Errorf("all modules: digest has no policy reminder:\n%s", body)
	}
}

// With Third-party risk switched off, no background sender reaches anyone
// about it, an admin included, and no notification kind it might own is
// counted. Vendor reviews have no reminder job today (#2175 decisions
// log); this guards the module answer any future one must ask.
func TestThirdPartyRiskOff_ReachesNoOne(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	flags := featureflag.NewStore(app)
	views := moduleaccess.NewViews(moduleaccess.NewStore(app), flags)

	tenantID, userID := seedUserWithType(t, admin, "vendor-admin@example.test", "", false)
	moduleSeed(t, admin, tenantID, userID, "admin", "evidence.staleness")
	ctx := tenantCtx(t, tenantID)

	if ok, err := views.Reaches(ctx, tenantID, userID.String(), "third_party_risk"); err != nil || !ok {
		t.Fatalf("switched on: Reaches = %v, %v; want true", ok, err)
	}
	if _, err := flags.Set(ctx, "vendor.enabled", false, "test", "#2175"); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	for _, key := range []string{"third_party_risk", "vendor_claims"} {
		if ok, err := views.Reaches(ctx, tenantID, userID.String(), key); err != nil || ok {
			t.Errorf("switched off: Reaches(admin, %s) = %v, %v; want false", key, ok, err)
		}
	}
}
