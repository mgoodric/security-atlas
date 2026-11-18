//go:build integration

// #2483 against real Postgres: a disabled user whose user_roles row is
// stored under a non-canonical spelling of their id gets no roles through
// that spelling. Before the fix the deactivation guard was skipped ($3 NULL)
// and the text match on user_id handed the role back.
//
// Run with:  go test -tags=integration -race -run 2483 ./internal/authz/...

package authz_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestDBRolesResolver2483_NonCanonicalRowOfDisabledUser(t *testing.T) {
	t.Parallel()
	appPool := dbtest.NewAppPool(t)
	adminPool := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, adminPool)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	u := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'u2483', 'disabled')`,
		u, tenant, "u2483-"+u.String()+"@example.invalid"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	keys := nonCanonical2483(u)
	for _, key := range keys {
		if _, err := adminPool.Exec(context.Background(),
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'test-2483')`,
			tenant, key); err != nil {
			t.Fatalf("seed user_roles %q: %v", key, err)
		}
	}
	r := authz.NewDBRolesResolver(appPool)
	for _, key := range keys {
		got, err := r.RolesFor(ctx, tenant, key)
		if err != nil {
			t.Fatalf("RolesFor(%q): %v", key, err)
		}
		if len(got) != 0 {
			t.Errorf("RolesFor(%q) = %v; want none for a disabled user", key, got)
		}
	}
	// Control: the same rows under an active user's id do match through the
	// canonical spelling, so the empty result above is the guard, not a
	// seeding mistake.
	if _, err := adminPool.Exec(context.Background(),
		`UPDATE users SET status = 'active' WHERE id = $1`, u); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'auditor', 'test-2483')`,
		tenant, u.String()); err != nil {
		t.Fatalf("seed canonical user_roles: %v", err)
	}
	got, err := r.RolesFor(ctx, tenant, u.String())
	if err != nil {
		t.Fatalf("RolesFor(canonical): %v", err)
	}
	if len(got) != 1 || got[0] != authz.RoleAuditor {
		t.Errorf("RolesFor(canonical) = %v; want [auditor]", got)
	}
}
