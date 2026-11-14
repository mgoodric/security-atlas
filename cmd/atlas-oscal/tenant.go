package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// tenantIDFlagHelp is the --tenant-id help shared by the import commands.
const tenantIDFlagHelp = "tenant UUID to import under; optional on a single-tenant install (default: the install's only tenant), required when several tenants exist"

// tenantResolver answers "which tenant is implied?" — in production
// tenancy.ResolveImplicitTenantAnyRole over the import's pool.
type tenantResolver func(ctx context.Context) (tenancy.TenantRef, error)

func poolTenantResolver(pool *pgxpool.Pool) tenantResolver {
	return func(ctx context.Context) (tenancy.TenantRef, error) {
		return tenancy.ResolveImplicitTenantAnyRole(ctx, pool)
	}
}

// importTenant picks the tenant an import writes under: flagValue when set,
// otherwise the install's only tenant, which it names on errOut. It never
// returns the all-zeros tenant, and when several tenants exist it refuses
// rather than guesses, listing each tenant's ID, slug and name (#1766, the
// same rule atlas-cli applies since #1762).
func importTenant(ctx context.Context, errOut io.Writer, flagValue string, resolve tenantResolver) (string, error) {
	if flagValue != "" {
		id, err := uuid.Parse(flagValue)
		if err != nil {
			return "", fmt.Errorf("--tenant-id %q is not a UUID: %w", flagValue, err)
		}
		if id == uuid.Nil {
			return "", errors.New("--tenant-id must not be the all-zeros tenant")
		}
		return id.String(), nil
	}
	ref, err := resolve(ctx)
	if err != nil {
		var amb *tenancy.AmbiguousTenantError
		switch {
		case errors.As(err, &amb):
			return "", fmt.Errorf("pass --tenant-id: %w", err)
		case errors.Is(err, tenancy.ErrNoTenants):
			return "", errors.New("no tenant exists: pass --tenant-id")
		}
		return "", fmt.Errorf("resolve tenant: %w", err)
	}
	_, _ = fmt.Fprintf(errOut, "tenant: %q, the install's only tenant\n", ref.Name)
	return ref.ID.String(), nil
}
