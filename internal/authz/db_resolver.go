package authz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DBRolesResolver looks up user_roles rows in Postgres. The pool is the
// RLS-enforced atlas_app pool, so the SELECT MUST run inside a
// transaction that has applied the `app.current_tenant` GUC — RLS
// returns zero rows otherwise.
type DBRolesResolver struct {
	pool *pgxpool.Pool
}

// NewDBRolesResolver constructs a resolver against pool.
func NewDBRolesResolver(pool *pgxpool.Pool) *DBRolesResolver {
	return &DBRolesResolver{pool: pool}
}

// RolesFor returns the roles assigned to (tenantID, userID). Returns
// (nil, nil) when no rows match -- the caller falls back to the
// credential-derived roles.
//
// userID is the credential's UserID -- "key_..." for slice-014/034
// machine credentials, or the user UUID for slice-034 human users.
// Both shapes are TEXT in user_roles.user_id, so no parsing.
//
// The SELECT runs inside a transaction that first applies the
// `app.current_tenant` GUC via tenancy.ApplyTenant. user_roles carries
// FORCE ROW LEVEL SECURITY; without the GUC the tenant_read policy
// matches nothing and the resolver silently returns zero roles, which
// then degrades every authenticated request to credential-only roles.
// Slice 065 fixed this alongside the audit-writer bug (#1): both shipped
// in slice 035 reading the RLS-enforced pool OUTSIDE a transaction.
func (r *DBRolesResolver) RolesFor(ctx context.Context, tenantID, userID string) ([]Role, error) {
	if r == nil || r.pool == nil {
		return nil, nil
	}
	if tenantID == "" || userID == "" {
		return nil, nil
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, fmt.Errorf("authz: tenant_id parse: %w", err)
	}
	const q = `
		SELECT role FROM user_roles
		WHERE tenant_id = $1::uuid AND user_id = $2
	`

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("authz: begin user_roles tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, fmt.Errorf("authz: apply tenant to user_roles tx: %w", err)
	}

	rows, err := tx.Query(ctx, q, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("authz: query user_roles: %w", err)
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		role := Role(s)
		if IsCanonical(role) {
			out = append(out, role)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
