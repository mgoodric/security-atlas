// user_resolver.go — DB-backed UserResolver impl for slice 189.
//
// The authorize handler captures the user's identity snapshot at
// code-issuance time so the redemption path can mint the JWT without
// re-reading the session. The snapshot includes:
//
//   - current_tenant_id (= the tenant the session was created under)
//   - available_tenants (= the full set of tenants the user belongs
//     to; v1 single-tenant-per-user keeps this as a one-element list,
//     but the field is plural so slice 192 multi-tenant work can
//     extend without a schema change)
//   - roles (per-tenant role lists)
//   - super_admin (currently FALSE for everyone except where an
//     admin override is present — v1 keeps super_admin as a deferred
//     escalation; slice 192 wires the real source)
//
// The resolver runs against the atlas_app pool (RLS enforced); the
// tenant GUC is applied via tenancy.ApplyTenant before reading the
// per-tenant user_roles rows.
package oauth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DBUserResolver is the production UserResolver implementation backed
// by Postgres. v1 keeps the resolution surface narrow — the JWT minted
// at redemption carries enough scope to support the slice-190 R2
// middleware; slice 192 multi-tenant work will extend this resolver.
type DBUserResolver struct {
	pool *pgxpool.Pool
}

// NewDBUserResolver constructs a resolver backed by pool. The pool
// is the atlas_app pool (RLS enforced); callers MUST NOT pass a
// BYPASSRLS pool because user_roles relies on the tenant GUC.
func NewDBUserResolver(pool *pgxpool.Pool) *DBUserResolver {
	return &DBUserResolver{pool: pool}
}

// ResolveForOAuth returns the UserIdentity snapshot the authorize
// handler captures into oauth_auth_codes. v1 implementation:
//
//   - current_tenant_id = the session's tenant
//   - available_tenants = []uuid.UUID{currentTenantID} (single-tenant
//     v1; slice 192 expands)
//   - roles = roles from user_roles for (current_tenant_id, user_id)
//   - super_admin = false (v1 — slice 192 wires the real source from
//     user attributes)
func (r *DBUserResolver) ResolveForOAuth(ctx context.Context, userID, tenantID uuid.UUID) (UserIdentity, error) {
	id := UserIdentity{
		UserID:           userID,
		CurrentTenantID:  tenantID,
		AvailableTenants: []uuid.UUID{tenantID},
		Roles:            map[uuid.UUID][]string{},
		SuperAdmin:       false,
	}
	if r == nil || r.pool == nil {
		return id, nil
	}

	tenantCtx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return UserIdentity{}, fmt.Errorf("oauth: with tenant: %w", err)
	}
	tx, err := r.pool.BeginTx(tenantCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return UserIdentity{}, fmt.Errorf("oauth: begin user_roles tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()
	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return UserIdentity{}, fmt.Errorf("oauth: apply tenant: %w", err)
	}

	rows, err := tx.Query(tenantCtx,
		`SELECT role FROM user_roles WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID.String(),
	)
	if err != nil {
		return UserIdentity{}, fmt.Errorf("oauth: query user_roles: %w", err)
	}
	defer rows.Close()

	roles := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return UserIdentity{}, fmt.Errorf("oauth: scan role: %w", err)
		}
		roles = append(roles, role)
	}
	if len(roles) > 0 {
		id.Roles[tenantID] = roles
	}
	return id, nil
}
