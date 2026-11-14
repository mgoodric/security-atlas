//go:build integration

// Integration tests for #2043: a demo seed's forensic mark never makes the
// actor's own organisation look like a demo tenant.
//
// The super-admin demo button passes the clicking admin's tenant as
// ActorTenantID. Before #2043 the seeder wrote its demo_seed_apply mark to
// that tenant's me_audit_log, so on a multi-tenant install the admin's real
// organisation carried the mark, and `demo teardown --tenant-slug=<org>`
// deleted the organisation.
//
// Run with: go test -tags=integration -p 1 ./internal/demoseed/...

package demoseed_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// assertTeardownRefusesAndOrgSurvives tears down org by slug and fails the
// test unless the teardown refuses and the org keeps its tenant row and
// every control it had.
func assertTeardownRefusesAndOrgSurvives(t *testing.T, seeder *demoseed.Seeder, orgID uuid.UUID, orgSlug string) {
	t.Helper()
	ctx := context.Background()
	controlsBefore := rowCount(t, "controls", orgID)

	err := seeder.Teardown(ctx, orgSlug, uuid.Nil, uuid.Nil)
	if err == nil || !strings.Contains(err.Error(), "does not carry the slice-205 forensic mark") {
		t.Errorf("Teardown(%q) err = %v, want a refusal", orgSlug, err)
	}
	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, orgID).Scan(&exists); err != nil {
		t.Fatalf("read tenant: %v", err)
	}
	if !exists {
		t.Fatalf("organisation %q was deleted by a demo teardown", orgSlug)
	}
	if got := rowCount(t, "controls", orgID); got != controlsBefore {
		t.Errorf("organisation controls = %d after teardown, want %d", got, controlsBefore)
	}
}

// The button path: Apply with the admin's real organisation as the actor
// tenant. The demo tenant can still be torn down; the organisation cannot.
func TestApply_ActorTenantMark_TeardownBySlugNeverDropsActorOrg(t *testing.T) {
	ctx := context.Background()
	org := freshInstallTenant(t)
	demoSlug := "demo-btn-" + uuid.NewString()[:8]
	cleanupTenant(t, demoSlug)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	res, err := seeder.Apply(ctx, demoseed.ApplyInput{
		Slug:          demoSlug,
		ActorUserID:   uuid.New(),
		ActorTenantID: org.ID,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var orgMarks int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM me_audit_log
		WHERE tenant_id = $1 AND action IN ('demo_seed_apply', 'demo_seed_teardown')
	`, org.ID).Scan(&orgMarks); err != nil {
		t.Fatalf("count org marks: %v", err)
	}
	if orgMarks != 0 {
		t.Errorf("organisation me_audit_log holds %d demo seed marks, want 0 (the mark belongs to the demo tenant)", orgMarks)
	}

	assertTeardownRefusesAndOrgSurvives(t, seeder, org.ID, org.Slug)

	if err := seeder.Teardown(ctx, demoSlug, uuid.Nil, org.ID); err != nil {
		t.Fatalf("Teardown(demo tenant): %v", err)
	}
	var demoExists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, res.TenantID).Scan(&demoExists); err != nil {
		t.Fatalf("read demo tenant: %v", err)
	}
	if demoExists {
		t.Errorf("demo tenant %q survived its own teardown", demoSlug)
	}
}

// Marks already written into an organisation by a button click before #2043
// name the demo tenant's slug, not the organisation's. They must not make the
// organisation tear-down-able either.
func TestTeardown_LegacyMarkInActorOrg_RefusesAndOrgSurvives(t *testing.T) {
	ctx := context.Background()
	org := freshInstallTenant(t)
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO me_audit_log (tenant_id, user_id, action, before, after)
		VALUES ($1, $2, 'demo_seed_apply', '{}'::jsonb, $3::jsonb)
	`, org.ID, uuid.New(), `{"slug": "demo", "demo_seed_v": "1.0.0"}`); err != nil {
		t.Fatalf("insert legacy mark: %v", err)
	}

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	assertTeardownRefusesAndOrgSurvives(t, seeder, org.ID, org.Slug)
}
