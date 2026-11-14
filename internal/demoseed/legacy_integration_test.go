//go:build integration

// Integration tests for #2153: removing a demo tenant the super-admin button
// created before #2043, whose demo_seed_apply mark sits in the clicking
// admin's organisation rather than in the demo tenant.
//
// Run with: go test -tags=integration -p 1 ./internal/demoseed/...

package demoseed_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// legacyFixture is an organisation with a super admin, beside a demo tenant
// in the shape a pre-#2043 button click left it.
type legacyFixture struct {
	orgID      uuid.UUID
	adminEmail string
	adminID    uuid.UUID
	demoSlug   string
	demoID     uuid.UUID
}

// newLegacyFixture seeds a demo tenant through Apply, then moves its mark
// where the pre-#2043 seeder wrote it: out of the demo tenant and into the
// organisation of the admin who clicked.
func newLegacyFixture(t *testing.T) legacyFixture {
	t.Helper()
	ctx := context.Background()
	org := freshInstallTenant(t)
	f := legacyFixture{orgID: org.ID, demoSlug: "demo-legacy-" + uuid.NewString()[:8]}
	f.adminID, f.adminEmail = grantSuperAdmin(t, org.ID)

	seeder := newSeeder(t)
	res, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: f.demoSlug, ActorUserID: f.adminID, ActorTenantID: org.ID})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	f.demoID = res.TenantID
	t.Cleanup(func() { removeTenant(t, f.demoID) })
	// The button ran the post-seed evaluator too (slice 671), so its
	// derived rows are part of the legacy tenant's shape.
	if _, err := demoseed.EvaluateSeededTenant(ctx, appPoolOrSkip(t), f.demoID); err != nil {
		t.Fatalf("EvaluateSeededTenant: %v", err)
	}

	if _, err := adminPool.Exec(ctx, `
		UPDATE me_audit_log SET tenant_id = $2
		WHERE tenant_id = $1 AND action = 'demo_seed_apply'
	`, f.demoID, org.ID); err != nil {
		t.Fatalf("move mark to the organisation: %v", err)
	}
	// The pre-#2043 state: Teardown refuses the demo tenant.
	if err := seeder.Teardown(ctx, f.demoSlug, uuid.Nil, uuid.Nil); err == nil ||
		!strings.Contains(err.Error(), "does not carry the slice-205 forensic mark") {
		t.Fatalf("Teardown of the legacy tenant err = %v, want the missing-mark refusal", err)
	}
	return f
}

// grantSuperAdmin adds an active user to tenantID and grants it super admin.
func grantSuperAdmin(t *testing.T, tenantID uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id, email := uuid.New(), "root-"+uuid.NewString()[:8]+"@solo.example"
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'Root', 'active')`,
		id, tenantID, email,
	); err != nil {
		t.Fatalf("insert super admin user: %v", err)
	}
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'bootstrap_first_install')`, id,
	); err != nil {
		t.Fatalf("grant super admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM super_admins WHERE user_id = $1`, id)
	})
	return id, email
}

func newSeeder(t *testing.T) *demoseed.Seeder {
	t.Helper()
	s, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	return s
}

func tenantExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var ok bool
	if err := adminPool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, id).Scan(&ok); err != nil {
		t.Fatalf("read tenant: %v", err)
	}
	return ok
}

func assertRefused(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, demoseed.ErrLegacyTeardownRefused) || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want a refusal containing %q", err, want)
	}
}

func TestTeardownLegacyButtonTenant_DeletesTheDemoTenantAndRecordsTheSuperAdmin(t *testing.T) {
	ctx := context.Background()
	f := newLegacyFixture(t)
	orgControls := rowCount(t, "controls", f.orgID)

	if err := newSeeder(t).TeardownLegacyButtonTenant(ctx, f.demoSlug, strings.ToUpper(f.adminEmail)); err != nil {
		t.Fatalf("TeardownLegacyButtonTenant: %v", err)
	}
	if tenantExists(t, f.demoID) {
		t.Error("legacy demo tenant survived its teardown")
	}
	if left := withoutAuditTables(tenantRows(t, f.demoID)); len(left) > 0 {
		t.Errorf("rows left behind for the deleted tenant: %v", left)
	}
	if !tenantExists(t, f.orgID) || rowCount(t, "controls", f.orgID) != orgControls {
		t.Error("the organisation lost its tenant row or controls")
	}
	var actor, actorTenant uuid.UUID
	err := adminPool.QueryRow(ctx, `
		SELECT actor_user_id, actor_tenant_id FROM super_admin_audit_log
		WHERE action = 'demo_seed_teardown' AND payload_json->>'slug' = $1
		  AND payload_json->>'legacy_button_tenant' = 'true'
	`, f.demoSlug).Scan(&actor, &actorTenant)
	if err != nil {
		t.Fatalf("read teardown audit row: %v", err)
	}
	if actor != f.adminID || actorTenant != f.orgID {
		t.Errorf("teardown audited as (%s, %s), want the super admin (%s, %s)", actor, actorTenant, f.adminID, f.orgID)
	}
}

func TestTeardownLegacyButtonTenant_RefusesANonSuperAdmin(t *testing.T) {
	ctx := context.Background()
	f := newLegacyFixture(t)
	s := newSeeder(t)

	// admin@solo.example is the organisation's admin, not a super admin.
	assertRefused(t, s.TeardownLegacyButtonTenant(ctx, f.demoSlug, "admin@solo.example"), "active super admins")
	assertRefused(t, s.TeardownLegacyButtonTenant(ctx, f.demoSlug, "  "), "super admin email is required")
	if _, err := adminPool.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, f.adminID); err != nil {
		t.Fatalf("disable super admin: %v", err)
	}
	assertRefused(t, s.TeardownLegacyButtonTenant(ctx, f.demoSlug, f.adminEmail), "active super admins")
	if !tenantExists(t, f.demoID) {
		t.Error("a refused teardown deleted the tenant")
	}
}

func TestTeardownLegacyButtonTenant_RefusesNonDemoData(t *testing.T) {
	ctx := context.Background()

	t.Run("a user that is not a demo user", func(t *testing.T) {
		f := newLegacyFixture(t)
		if _, err := adminPool.Exec(ctx,
			`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, 'real@corp.example', 'Real', 'active')`,
			uuid.New(), f.demoID,
		); err != nil {
			t.Fatalf("insert real user: %v", err)
		}
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, f.demoSlug, f.adminEmail), "not demo users")
		if !tenantExists(t, f.demoID) {
			t.Error("a refused teardown deleted the tenant")
		}
	})

	// security_training_people is real-programme data the seed never writes.
	t.Run("a row in a table the seed does not write", func(t *testing.T) {
		f := newLegacyFixture(t)
		if _, err := adminPool.Exec(ctx,
			`INSERT INTO security_training_people (tenant_id, display_name) VALUES ($1, 'Real Person')`, f.demoID,
		); err != nil {
			t.Fatalf("insert training person: %v", err)
		}
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, f.demoSlug, f.adminEmail), "security_training_people")
		if !tenantExists(t, f.demoID) {
			t.Error("a refused teardown deleted the tenant")
		}
	})
}

func TestTeardownLegacyButtonTenant_RefusesTenantsTheOrdinaryTeardownOwns(t *testing.T) {
	ctx := context.Background()

	t.Run("an organisation with no legacy mark", func(t *testing.T) {
		org := freshInstallTenant(t)
		_, email := grantSuperAdmin(t, org.ID)
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, org.Slug, email), "no pre-#2043 demo button mark")
		if !tenantExists(t, org.ID) {
			t.Error("a refused teardown deleted the organisation")
		}
	})

	t.Run("the organisation holding the legacy mark", func(t *testing.T) {
		f := newLegacyFixture(t)
		var orgSlug string
		if err := adminPool.QueryRow(ctx, `SELECT slug FROM tenants WHERE id = $1`, f.orgID).Scan(&orgSlug); err != nil {
			t.Fatalf("read org slug: %v", err)
		}
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, orgSlug, f.adminEmail), "no pre-#2043 demo button mark")
		if !tenantExists(t, f.orgID) {
			t.Error("a refused teardown deleted the organisation")
		}
	})

	t.Run("a demo tenant with its own mark", func(t *testing.T) {
		f := newLegacyFixture(t)
		slug := "demo-marked-" + uuid.NewString()[:8]
		cleanupTenant(t, slug)
		if _, err := newSeeder(t).Apply(ctx, demoseed.ApplyInput{Slug: slug, ActorUserID: f.adminID, ActorTenantID: f.orgID}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, slug, f.adminEmail), "carries its own demo seed mark")
	})

	t.Run("a tenant seeded in place", func(t *testing.T) {
		ref := freshInstallTenant(t)
		if _, err := soleTenantSeeder(t, ref).Seed(ctx, demoseed.SeedInput{}); err != nil {
			t.Fatalf("Seed in place: %v", err)
		}
		_, email := grantSuperAdmin(t, ref.ID)
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, ref.Slug, email), "seeded in place")
		if !tenantExists(t, ref.ID) {
			t.Error("a refused teardown deleted the organisation")
		}
	})

	t.Run("an unknown slug", func(t *testing.T) {
		f := newLegacyFixture(t)
		assertRefused(t, newSeeder(t).TeardownLegacyButtonTenant(ctx, "no-such-"+uuid.NewString()[:8], f.adminEmail), "not found")
	})
}
