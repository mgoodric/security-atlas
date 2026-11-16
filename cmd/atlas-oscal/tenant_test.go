package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Tests for #1766: the import commands' --tenant-id is optional on a
// single-tenant install and refused, with the tenants listed, on a
// multi-tenant one. The resolver stands in for the tenants table here;
// tenancy.ResolveImplicitTenantAnyRole runs against real Postgres in
// internal/tenancy's integration suite.

var (
	soloTenant = tenancy.TenantRef{ID: uuid.MustParse("6f1c1f43-6c55-4cf6-9d5e-1b8f0c1a0001"), Slug: "solo-org", Name: "Solo Org"}
	acmeTenant = tenancy.TenantRef{ID: uuid.MustParse("6f1c1f43-6c55-4cf6-9d5e-1b8f0c1a0002"), Slug: "acme", Name: "Acme Corp"}
)

// tenantsResolver answers as tenancy.ResolveImplicitTenant would over tenants.
func tenantsResolver(tenants ...tenancy.TenantRef) tenantResolver {
	return func(context.Context) (tenancy.TenantRef, error) {
		switch len(tenants) {
		case 0:
			return tenancy.TenantRef{}, tenancy.ErrNoTenants
		case 1:
			return tenants[0], nil
		}
		return tenancy.TenantRef{}, &tenancy.AmbiguousTenantError{Tenants: tenants}
	}
}

func TestImportTenant_SingleTenant_NoFlag_UsesOnlyTenant(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	got, err := importTenant(context.Background(), &errOut, "", tenantsResolver(soloTenant))
	if err != nil {
		t.Fatalf("importTenant: %v", err)
	}
	if got != soloTenant.ID.String() {
		t.Errorf("tenant = %s, want %s", got, soloTenant.ID)
	}
	if !strings.Contains(errOut.String(), `"Solo Org"`) {
		t.Errorf("stderr = %q, want it to name the tenant used", errOut.String())
	}
}

func TestImportTenant_MultiTenant_NoFlag_ListsTenants(t *testing.T) {
	t.Parallel()
	_, err := importTenant(context.Background(), &bytes.Buffer{}, "", tenantsResolver(soloTenant, acmeTenant))
	if err == nil {
		t.Fatal("expected an error with several tenants and no --tenant-id")
	}
	for _, want := range []string{"--tenant-id", soloTenant.ID.String(), "acme", "Acme Corp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestImportTenant_NoTenants_AsksForFlag(t *testing.T) {
	t.Parallel()
	_, err := importTenant(context.Background(), &bytes.Buffer{}, "", tenantsResolver())
	if err == nil || !strings.Contains(err.Error(), "no tenant exists") {
		t.Fatalf("err = %v, want a no-tenant error", err)
	}
}

func TestImportTenant_ResolverFailure_IsWrapped(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection refused")
	_, err := importTenant(context.Background(), &bytes.Buffer{}, "",
		func(context.Context) (tenancy.TenantRef, error) { return tenancy.TenantRef{}, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the resolver error", err)
	}
}

func TestImportTenant_FlagGiven_SkipsResolver(t *testing.T) {
	t.Parallel()
	called := false
	resolve := func(context.Context) (tenancy.TenantRef, error) {
		called = true
		return soloTenant, nil
	}
	got, err := importTenant(context.Background(), &bytes.Buffer{}, acmeTenant.ID.String(), resolve)
	if err != nil {
		t.Fatalf("importTenant: %v", err)
	}
	if got != acmeTenant.ID.String() || called {
		t.Errorf("tenant = %s (resolver called: %v), want %s without resolving", got, called, acmeTenant.ID)
	}
}

func TestImportTenant_RejectsBadFlag(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"not-a-uuid", uuid.Nil.String()} {
		if _, err := importTenant(context.Background(), &bytes.Buffer{}, v, tenantsResolver(soloTenant)); err == nil {
			t.Errorf("--tenant-id %q: expected an error", v)
		}
	}
}
