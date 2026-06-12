//go:build integration

// Integration tests for the slice 509 mapping CRUD store (AC-8) + its RLS
// confinement. Proves an admin of tenant A cannot read or delete tenant B's
// mappings (cross-tenant isolation, invariant #6).
package grouprole_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
)

func TestStore_CRUD(t *testing.T) {
	requireAdminPool(t)
	tenant := seedTenant(t, "gr-crud")
	store := grouprole.NewStore(appPool)
	ctx := tenantCtx(t, tenant)

	// Create (SCIM source — idp_config nil).
	m, err := store.Create(ctx, grouprole.CreateMappingInput{GroupRef: "SecTeam", Role: "auditor"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.Role != "auditor" || m.GroupRef != "SecTeam" || m.IDPConfigID != nil {
		t.Fatalf("unexpected mapping: %+v", m)
	}

	// Create idempotent on the unique index.
	if _, err := store.Create(ctx, grouprole.CreateMappingInput{GroupRef: "SecTeam", Role: "auditor"}); err != nil {
		t.Fatalf("idempotent create: %v", err)
	}

	// List shows exactly one row.
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d rows; want 1 (idempotent)", len(list))
	}

	// P0-509-4: a mapping to a non-existent role is rejected at the store.
	if _, err := store.Create(ctx, grouprole.CreateMappingInput{GroupRef: "X", Role: "superuser"}); err == nil {
		t.Fatal("expected unknown-role rejection (P0-509-4)")
	}

	// Delete.
	if err := store.Delete(ctx, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.Delete(ctx, m.ID); err != grouprole.ErrMappingNotFound {
		t.Fatalf("re-delete err = %v; want ErrMappingNotFound", err)
	}
}

func TestStore_CrossTenantRLS(t *testing.T) {
	requireAdminPool(t)
	tenantA := seedTenant(t, "gr-crud-A")
	tenantB := seedTenant(t, "gr-crud-B")
	store := grouprole.NewStore(appPool)

	// Tenant A creates a mapping.
	mA, err := store.Create(tenantCtx(t, tenantA), grouprole.CreateMappingInput{GroupRef: "Admins", Role: "admin"})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}

	// Tenant B's List must NOT see tenant A's mapping (RLS).
	listB, err := store.List(tenantCtx(t, tenantB))
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("tenant B saw %d of tenant A's mappings (RLS breach)", len(listB))
	}

	// Tenant B cannot delete tenant A's mapping (RLS scopes the DELETE to B).
	if err := store.Delete(tenantCtx(t, tenantB), mA.ID); err != grouprole.ErrMappingNotFound {
		t.Fatalf("tenant B delete of A's mapping err = %v; want ErrMappingNotFound", err)
	}

	// Tenant A still has its mapping.
	listA, err := store.List(tenantCtx(t, tenantA))
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("tenant A lost its mapping after B's delete attempt: %d", len(listA))
	}
}
