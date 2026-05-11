// Package tenancy carries the current request's tenant identity through
// context.Context and applies it to a database transaction as the Postgres
// GUC `app.current_tenant`. The Row-Level Security policies on every
// tenant-scoped table compare `tenant_id::text` against this GUC; if it is
// unset, comparisons fail and rows are excluded — there is no default-allow
// path. See migrations/sql/20260511000000_init.sql.
package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// GUCName is the Postgres custom GUC that carries the current tenant id.
// Row-Level Security policies on every tenant-scoped table compare against
// it via current_setting(GUCName, true).
const GUCName = "app.current_tenant"

type ctxKey struct{}

// ErrNoTenant is returned by TenantFromContext when no tenant has been
// attached to the context. Callers must surface this as an explicit error
// (HTTP 400/401-shaped) — RLS will deny anyway, but the application should
// fail loudly upstream rather than silently returning zero rows.
var ErrNoTenant = errors.New("tenancy: no tenant in context")

// WithTenant returns a derived context tagged with tenantID. The tenantID
// must be a valid UUID; non-UUID strings are rejected so a malformed input
// cannot quietly bypass RLS (a string Postgres rejects mid-tx would surface
// as a query error, but failing here is faster and more localized).
func WithTenant(ctx context.Context, tenantID string) (context.Context, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return ctx, fmt.Errorf("tenancy: invalid tenant id %q: %w", tenantID, err)
	}
	return context.WithValue(ctx, ctxKey{}, tenantID), nil
}

// TenantFromContext extracts the tenant id previously attached with
// WithTenant. Returns ErrNoTenant if no tenant is set.
func TenantFromContext(ctx context.Context) (string, error) {
	v, ok := ctx.Value(ctxKey{}).(string)
	if !ok || v == "" {
		return "", ErrNoTenant
	}
	return v, nil
}
