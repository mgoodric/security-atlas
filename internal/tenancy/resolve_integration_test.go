//go:build integration

// Integration test for #1762: ResolveImplicitTenantAnyRole over the
// RLS-bound atlas_app pool sees every tenant, as the BYPASSRLS migrate pool
// does, so the CLI's database-backed commands can resolve the implicit
// tenant from the DSN they already hold.
//
// Run via: go test -tags=integration ./internal/tenancy/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package tenancy_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func integrationPools(t *testing.T) (app, admin *pgxpool.Pool) {
	t.Helper()
	appURL, adminURL := os.Getenv("DATABASE_URL_APP"), os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		t.Skip("DATABASE_URL_APP or DATABASE_URL not set; skipping tenancy integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	admin, err = pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(func() { app.Close(); admin.Close() })
	return app, admin
}

// insertTenant adds a tenant and removes it when the test ends. The test
// never deletes tenants it did not create, so it is safe in a shared
// database whatever other tenants exist.
func insertTenant(t *testing.T, admin *pgxpool.Pool, name, slug string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`, id, name, slug); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// The app pool's answer matches the migrate pool's, whatever tenants the
// database already holds: the role switch makes atlas_app see them all.
func TestResolveImplicitTenantAnyRole_AppRoleSeesWhatMigrateRoleSees(t *testing.T) {
	app, admin := integrationPools(t)
	ctx := context.Background()
	insertTenant(t, admin, "Solo Org", "solo-"+uuid.NewString()[:8])

	want, wantErr := tenancy.ResolveImplicitTenant(ctx, admin)
	for name, pool := range map[string]*pgxpool.Pool{"app": app, "migrate": admin} {
		got, err := tenancy.ResolveImplicitTenantAnyRole(ctx, pool)
		if errors.Is(err, tenancy.ErrNoTenants) {
			t.Fatalf("%s pool: ErrNoTenants, want the tenants the migrate role sees", name)
		}
		var wantAmb, gotAmb *tenancy.AmbiguousTenantError
		switch {
		case errors.As(wantErr, &wantAmb):
			if !errors.As(err, &gotAmb) || len(gotAmb.Tenants) != len(wantAmb.Tenants) {
				t.Errorf("%s pool: err = %v, want %v", name, err, wantErr)
			}
		case wantErr != nil:
			t.Fatalf("migrate pool resolve: %v", wantErr)
		case err != nil || got != want:
			t.Errorf("%s pool: got %+v, %v; want %+v", name, got, err, want)
		}
	}
}

// With two tenants of its own present, the app pool's answer lists both
// by ID, slug and name rather than picking one.
func TestResolveImplicitTenantAnyRole_AppRoleListsSeveralTenants(t *testing.T) {
	app, admin := integrationPools(t)
	slugA, slugB := "acme-"+uuid.NewString()[:8], "beta-"+uuid.NewString()[:8]
	a := insertTenant(t, admin, "Acme Corp", slugA)
	b := insertTenant(t, admin, "Beta Ltd", slugB)

	_, err := tenancy.ResolveImplicitTenantAnyRole(context.Background(), app)
	var amb *tenancy.AmbiguousTenantError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want *AmbiguousTenantError", err)
	}
	found := map[uuid.UUID]tenancy.TenantRef{}
	for _, tn := range amb.Tenants {
		found[tn.ID] = tn
	}
	for id, want := range map[uuid.UUID]tenancy.TenantRef{
		a: {ID: a, Slug: slugA, Name: "Acme Corp"},
		b: {ID: b, Slug: slugB, Name: "Beta Ltd"},
	} {
		if found[id] != want {
			t.Errorf("tenant %s listed as %+v, want %+v", id, found[id], want)
		}
	}
}

// One real tenant alone (beside the all-zeros row) is the implied tenant
// for the app pool. Runs only when the database holds no other tenant, so
// it never deletes another test's data.
func TestResolveImplicitTenantAnyRole_AppRoleSingleTenant(t *testing.T) {
	app, admin := integrationPools(t)
	ctx := context.Background()
	if _, err := tenancy.ResolveImplicitTenant(ctx, admin); !errors.Is(err, tenancy.ErrNoTenants) {
		t.Skipf("database already holds tenants (%v); the shared-DB comparison test covers this case", err)
	}
	only := insertTenant(t, admin, "Solo Org", "solo-"+uuid.NewString()[:8])
	got, err := tenancy.ResolveImplicitTenantAnyRole(ctx, app)
	if err != nil || got.ID != only {
		t.Fatalf("got %+v, %v; want tenant %s", got, err, only)
	}
}
