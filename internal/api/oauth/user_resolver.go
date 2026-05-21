// user_resolver.go — DB-backed UserResolver impl.
//
// Slice 189 introduced the resolver as a single-tenant snapshot:
// `available_tenants = [currentTenantID]`. Slice 192 expands it to
// enumerate the user's actual multi-tenant membership so the JWT
// minted at code-redemption carries the full set the tenant-switcher
// dropdown reads from. The expansion is the load-bearing back-end
// change for slice 192's vCISO use case (one operator → N tenants).
//
// The cross-tenant enumeration query MUST be unscoped by tenant —
// the operator hits /oauth/authorize before the JWT carries their
// available_tenants, so we cannot use the RLS-bound atlas_app pool
// to enumerate. v1 resolution: take an optional authPool (the
// BYPASSRLS `atlas_migrate` pool already wired in cmd/atlas/main.go
// for similar identity-enumeration paths — see slice 034's
// apikey-by-hash lookup at line 506 of main.go). When authPool is
// nil (unit tests + the in-memory cmd/atlas mode), the resolver
// falls back to the slice-189 single-tenant snapshot so existing
// tests stay green and the in-memory mode remains functional.
//
// SLICE 192 INVARIANTS HONORED:
//
//   - P0-192-1 / canvas §11 #13: the resolver populates
//     available_tenants accurately so the frontend can HIDE the
//     switcher chrome when the array has length 1.
//   - P0-192-2: `/v1/me/tenants` reads the JWT claim populated here.
//   - P0-192-5: the login picker sources its tenant list from the
//     `users` table joined per OIDC identity, not from `tenants`.
//   - P0-192-6: does NOT modify slice 188's token-exchange handler.
//   - P0-192-10: does NOT modify slice 187's keystore + tokensign +
//     jwt packages.
package oauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DBUserResolver is the production UserResolver implementation backed
// by Postgres.
//
//   - pool      : the RLS-bound `atlas_app` pool — per-tenant
//     user_roles SELECT (RLS enforces tenant scope via GUC).
//   - authPool  : the BYPASSRLS `atlas_migrate` pool (optional) —
//     used for the cross-tenant `users` lookup that enumerates the
//     OIDC subject's tenant memberships. Nil → slice-189
//     single-tenant snapshot.
type DBUserResolver struct {
	pool     *pgxpool.Pool
	authPool *pgxpool.Pool
}

// NewDBUserResolver constructs a resolver. authPool MAY be nil — in
// that case the resolver returns the slice-189 single-tenant
// snapshot (which the in-memory test harness relies on).
func NewDBUserResolver(pool *pgxpool.Pool) *DBUserResolver {
	return &DBUserResolver{pool: pool}
}

// NewDBUserResolverWithAuthPool is the slice-192 constructor that
// also wires the BYPASSRLS pool for the cross-tenant `users`
// enumeration.
func NewDBUserResolverWithAuthPool(pool, authPool *pgxpool.Pool) *DBUserResolver {
	return &DBUserResolver{pool: pool, authPool: authPool}
}

// userMembership is the per-tenant row returned by the cross-tenant
// enumeration query — the user's tenant id PLUS the
// tenant-scoped users.id (different from the session-tenant's users.id
// because slice 034's users table keys on per-tenant UUIDs).
type userMembership struct {
	tenantID uuid.UUID
	userID   uuid.UUID
}

// ResolveForOAuth returns the UserIdentity snapshot the authorize
// handler captures into oauth_auth_codes.
//
// Slice 192 expansion:
//
//   - current_tenant_id = the session's tenant (unchanged).
//   - available_tenants = the FULL set of tenants the OIDC subject
//     belongs to via the cross-tenant `users` query.
//   - roles = a map keyed on tenant_id; populated from per-tenant
//     user_roles queries under the RLS-bound pool.
//   - super_admin = false (no super_admins table at v2; spillover
//     slice 198 ships the OIDC-first-install bootstrap path).
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

	// Step 1 — read the session-tenant user row's
	// (idp_issuer, idp_subject) under RLS.
	idpIssuer, idpSubject, err := r.readSessionIdentity(ctx, userID, tenantID)
	if err != nil {
		return UserIdentity{}, fmt.Errorf("oauth: read session identity: %w", err)
	}

	// Step 2 — when we have an IdP-backed identity AND a BYPASSRLS
	// pool, enumerate cross-tenant memberships. The membership rows
	// carry (tenant_id, user_id) so step 3 can resolve roles without
	// a second per-tenant identity lookup (N+1 elimination).
	memberships := []userMembership{{tenantID: tenantID, userID: userID}}
	if r.authPool != nil && idpIssuer != "" && idpSubject != "" {
		ms, mErr := r.enumerateMemberships(ctx, idpIssuer, idpSubject, tenantID, userID)
		if mErr != nil {
			return UserIdentity{}, fmt.Errorf("oauth: enumerate memberships: %w", mErr)
		}
		memberships = ms
	}

	id.AvailableTenants = make([]uuid.UUID, 0, len(memberships))
	for _, m := range memberships {
		id.AvailableTenants = append(id.AvailableTenants, m.tenantID)
	}

	// Step 3 — per-tenant role lookup, using the membership row's
	// tenant-scoped user_id (no second identity query).
	for _, m := range memberships {
		roles, rErr := r.queryUserRoles(ctx, m.tenantID, m.userID)
		if rErr != nil {
			return UserIdentity{}, fmt.Errorf("oauth: query roles for tenant %s: %w", m.tenantID, rErr)
		}
		if len(roles) > 0 {
			id.Roles[m.tenantID] = roles
		}
	}

	return id, nil
}

// readSessionIdentity reads (idp_issuer, idp_subject) of the user row
// in the session tenant under the RLS-bound pool. Returns empty
// strings for local-mode users (no IdP backing) — the caller treats
// this as "single-tenant by definition".
func (r *DBUserResolver) readSessionIdentity(ctx context.Context, userID, sessionTenant uuid.UUID) (string, string, error) {
	tenantCtx, err := tenancy.WithTenant(ctx, sessionTenant.String())
	if err != nil {
		return "", "", fmt.Errorf("with tenant: %w", err)
	}
	tx, err := r.pool.BeginTx(tenantCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()
	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return "", "", fmt.Errorf("apply tenant: %w", err)
	}
	var idpIssuer, idpSubject string
	err = tx.QueryRow(tenantCtx,
		`SELECT idp_issuer, idp_subject FROM users WHERE id = $1`,
		userID,
	).Scan(&idpIssuer, &idpSubject)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return idpIssuer, idpSubject, nil
}

// enumerateMemberships returns one row per (tenant_id, users.id) for
// the OIDC subject. The session tenant + userID are appended as a
// fallback when the cross-tenant query misses the session (race
// window between session issue + tenant deletion).
//
// DISTINCT collapses the rare case where a user has multiple users
// rows in the same tenant (the slice 034
// `users_email_per_tenant_unique` constraint prevents it, but
// DISTINCT defends against future schema drift).
func (r *DBUserResolver) enumerateMemberships(
	ctx context.Context,
	idpIssuer, idpSubject string,
	sessionTenant, sessionUserID uuid.UUID,
) ([]userMembership, error) {
	rows, err := r.authPool.Query(ctx,
		`SELECT DISTINCT tenant_id, id FROM users
		 WHERE idp_issuer = $1 AND idp_subject = $2 AND status = 'active'
		 ORDER BY tenant_id ASC`,
		idpIssuer, idpSubject,
	)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	out := make([]userMembership, 0, 4)
	sawSession := false
	for rows.Next() {
		var m userMembership
		if err := rows.Scan(&m.tenantID, &m.userID); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		if m.tenantID == sessionTenant {
			sawSession = true
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	if !sawSession {
		out = append(out, userMembership{tenantID: sessionTenant, userID: sessionUserID})
	}
	return out, nil
}

// queryUserRoles runs the per-tenant user_roles SELECT under the
// RLS-bound atlas_app pool. The tenant-scoped userID was resolved at
// enumerate-memberships time (or, for the fallback path, is the
// session userID).
func (r *DBUserResolver) queryUserRoles(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
	tenantCtx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return nil, fmt.Errorf("with tenant: %w", err)
	}
	tx, err := r.pool.BeginTx(tenantCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()
	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return nil, fmt.Errorf("apply tenant: %w", err)
	}

	rows, err := tx.Query(tenantCtx,
		`SELECT role FROM user_roles WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	roles := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		roles = append(roles, role)
	}
	return roles, nil
}
