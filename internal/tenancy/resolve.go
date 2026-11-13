package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TenantRef identifies one tenant for the implicit-tenant resolver and for
// the error it returns when the choice is ambiguous. Slug is empty on legacy
// rows that predate slice 143's slug column.
type TenantRef struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// ErrNoTenants is returned by ResolveImplicitTenant when the install has no
// tenant at all (a genuinely fresh install).
var ErrNoTenants = errors.New("tenancy: no tenant exists")

// AmbiguousTenantError is returned by ResolveImplicitTenant when more than
// one tenant exists, so no tenant is implied. It lists every tenant so the
// caller can tell the operator what to choose from.
type AmbiguousTenantError struct {
	Tenants []TenantRef
}

func (e *AmbiguousTenantError) Error() string {
	parts := make([]string, 0, len(e.Tenants))
	for _, t := range e.Tenants {
		slug := t.Slug
		if slug == "" {
			slug = "-"
		}
		parts = append(parts, fmt.Sprintf("%s (slug %s, name %q)", t.ID, slug, t.Name))
	}
	return fmt.Sprintf("tenancy: %d tenants exist, choose one explicitly: %s",
		len(e.Tenants), strings.Join(parts, "; "))
}

// Querier is the slice of pgx shared by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ResolveImplicitTenant answers "which tenant does this request or command
// implicitly mean?". With exactly one tenant it returns that tenant; with
// none it returns ErrNoTenants; with several it returns *AmbiguousTenantError
// listing them. It never returns the all-zeros tenant: that row, if present,
// is not a tenant anyone means.
//
// tenants is under FORCE ROW LEVEL SECURITY, and the question is asked
// before any tenant is known, so q must be a BYPASSRLS connection (the
// atlas_migrate pool, or a transaction on it). Under an RLS-bound role the
// query sees at most the current tenant and the answer is wrong.
func ResolveImplicitTenant(ctx context.Context, q Querier) (TenantRef, error) {
	rows, err := q.Query(ctx,
		`SELECT id, COALESCE(slug, ''), name FROM tenants WHERE id <> $1 ORDER BY created_at, id`,
		uuid.Nil)
	if err != nil {
		return TenantRef{}, fmt.Errorf("tenancy: list tenants: %w", err)
	}
	tenants, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TenantRef, error) {
		var t TenantRef
		err := r.Scan(&t.ID, &t.Slug, &t.Name)
		return t, err
	})
	if err != nil {
		return TenantRef{}, fmt.Errorf("tenancy: scan tenants: %w", err)
	}
	return pickImplicitTenant(tenants)
}

func pickImplicitTenant(tenants []TenantRef) (TenantRef, error) {
	switch len(tenants) {
	case 0:
		return TenantRef{}, ErrNoTenants
	case 1:
		return tenants[0], nil
	default:
		return TenantRef{}, &AmbiguousTenantError{Tenants: tenants}
	}
}
