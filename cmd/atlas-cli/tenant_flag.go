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

// tenantResolver answers "which tenant is implied?" — in production
// tenancy.ResolveImplicitTenantAnyRole over the CLI's pool.
type tenantResolver func(ctx context.Context) (tenancy.TenantRef, error)

func poolTenantResolver(pool *pgxpool.Pool) tenantResolver {
	return func(ctx context.Context) (tenancy.TenantRef, error) {
		return tenancy.ResolveImplicitTenantAnyRole(ctx, pool)
	}
}

// newTenantResolver builds the resolver the database-backed commands
// (evidence verify, policies seed, oscal-export) use when their tenant
// flag is omitted (#1762). Tests replace it to stand in for the tenants
// table.
var newTenantResolver = poolTenantResolver

// tenantFlagHelp is the help text shared by the optional tenant flags.
const tenantFlagHelp = "tenant UUID; optional on a single-tenant install (default: the install's only tenant), required when several tenants exist"

// chooseTenant picks the tenant a command acts in: flagValue when set,
// otherwise the install's only tenant, which it names on errOut. It never
// returns uuid.Nil, and when several tenants exist it refuses rather than
// guesses, listing each tenant's ID, slug and name (#1739, #1762).
func chooseTenant(ctx context.Context, errOut io.Writer, flagName, flagValue string, resolve tenantResolver) (uuid.UUID, error) {
	if flagValue != "" {
		id, err := uuid.Parse(flagValue)
		if err != nil {
			return uuid.Nil, fmt.Errorf("%s %q is not a UUID: %w", flagName, flagValue, err)
		}
		if id == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%s must not be the all-zeros tenant", flagName)
		}
		return id, nil
	}
	ref, err := resolve(ctx)
	if err != nil {
		var amb *tenancy.AmbiguousTenantError
		switch {
		case errors.As(err, &amb):
			return uuid.Nil, fmt.Errorf("pass %s: %w", flagName, err)
		case errors.Is(err, tenancy.ErrNoTenants):
			return uuid.Nil, fmt.Errorf("no tenant exists: pass %s", flagName)
		}
		return uuid.Nil, fmt.Errorf("resolve tenant: %w", err)
	}
	_, _ = fmt.Fprintf(errOut, "tenant: %q, the install's only tenant\n", ref.Name)
	return ref.ID, nil
}
