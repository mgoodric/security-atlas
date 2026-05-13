package authz

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBRolesResolver looks up user_roles rows in Postgres. The pool MUST
// be used with a context that carries app.current_tenant set by the
// slice-033 tenancy middleware; RLS denies otherwise.
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
	rows, err := r.pool.Query(ctx, q, tenantID, userID)
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
		r := Role(s)
		if IsCanonical(r) {
			out = append(out, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
