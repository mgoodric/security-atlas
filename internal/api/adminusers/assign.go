// assign.go — slice 478: super-admin user↔tenant↔role assignment surface.
//
// This file EXTENDS the slice-062 adminusers package (handler.go: the
// within-tenant List/Get/PatchRoles) with the cross-tenant super-admin
// surface:
//
//	GET    /v1/admin/users              -- (extended) super-admin sees ALL
//	                                        tenants; tenant-admin sees own.
//	POST   /v1/admin/users/assign       -- assign a user identity to a tenant
//	                                        with role(s); creates the per-tenant
//	                                        membership + user_roles atomically;
//	                                        idempotent. Self-assign uses the same
//	                                        path with the actor as the target.
//	POST   /v1/admin/users/revoke       -- remove a user's roles in a tenant
//	                                        (optionally soft-disable membership).
//
// AUTHORITY TIERS (slice 478 D3):
//
//   - Cross-tenant (target tenant != session tenant, or the cross-tenant
//     list) requires jwtmw.FromContext().SuperAdmin. Enforced by
//     requireSuperAdmin. The write path uses the BYPASSRLS authPool (the
//     target tenant_id != the actor's GUC, so atlas_app RLS would block the
//     INSERT) — mirrors admintenants.Create (slice 143 D2).
//   - Within-tenant (target tenant == session tenant) is allowed for a
//     tenant-admin (cred.IsAdmin) and runs under RLS via atlas_app. A
//     tenant-admin can NEVER name a tenant other than their own
//     (P0-478-1) — the handler rejects a cross-tenant target for a
//     non-super-admin with 403.
//
// LOCAL-AUTH IDENTITY (slice 478 D1, THE load-bearing call):
//
//	A user's cross-tenant visibility derives from the slice-192 resolver's
//	enumerateMemberships: SELECT ... FROM users WHERE idp_issuer=$1 AND
//	idp_subject=$2 AND status='active'. Local operators have EMPTY (idp_issuer,
//	idp_subject). Copying the empty tuple into a second-tenant membership row
//	would make enumerateMemberships('','') match EVERY local user across ALL
//	tenants (P0-478-2 / the slice-476 hazard). So when the assign target is a
//	local user (empty IdP tuple) we mint a STABLE SYNTHETIC per-user key:
//	  idp_issuer  = 'urn:atlas:local'
//	  idp_subject = <origin users.id UUID string>
//	and write it to BOTH the new membership row AND (backfill) the origin row,
//	in the same transaction. The pair is non-empty (satisfies the resolver's
//	non-empty guard + the users_idp_principal_unique partial index) and unique
//	per local user (cannot over-match another local user). Local login is
//	unchanged — the synthetic issuer is never used for token exchange; the
//	user still authenticates via local_credentials keyed on user_id.
//
// Anti-criteria honored (P0-478-*):
//
//   - P0-478-1: a tenant-admin can only write within their own session
//     tenant; cross-tenant requires super_admin. Tested with a DENIED case.
//   - P0-478-2: local-auth synthetic key never over-matches the empty tuple.
//   - P0-478-3: P0-192-5 preserved — this surface only CREATES memberships;
//     the resolver/switcher stay membership-bounded for non-super-admins.
//   - P0-478-4: authority enforced server-side (requireSuperAdmin /
//     requireAdmin), not in the UI.
//   - P0-478-5: NO invitation/email/SCIM/password; NO auto-switch of the
//     actor's session tenant.
//   - P0-478-6: tests use neutral test-* fixtures only.
package adminusers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	// LocalSyntheticIssuer is the reserved synthetic OIDC issuer used as the
	// idp_issuer of a local user's cross-tenant membership rows (slice 478
	// D1). It is NOT a real OIDC issuer — it is never used for token
	// exchange or login; it exists solely as a stable, non-empty
	// enumeration key so the slice-192 resolver surfaces the local user's
	// multi-tenant memberships without the empty-tuple over-match.
	LocalSyntheticIssuer = "urn:atlas:local"

	// auditActionAssign / auditActionRevoke are the action values written to
	// me_audit_log (always) and super_admin_audit_log (cross-tenant path
	// only). Migration 20260607010000 admits both in both CHECKs.
	auditActionAssign = "user_tenant_assign"
	auditActionRevoke = "user_tenant_revoke"

	// assignRoleGrantedBy is the granted_by string the user_roles rows carry.
	assignRoleGrantedBy = "admin:user_assign"

	// maxAssignBody caps the assign/revoke request body.
	maxAssignBody = 16 * 1024
)

// SetAuthPool wires the BYPASSRLS auth pool for the cross-tenant assign /
// revoke / cross-tenant-list paths. When nil (unit-server harness without
// DATABASE_URL), cross-tenant operations return 503 and within-tenant
// operations continue to work via the RLS-bound pool. Returns the handler so
// it composes with New(...).
func (h *Handler) SetAuthPool(authPool *pgxpool.Pool) *Handler {
	h.authPool = authPool
	return h
}

// --- wire types ---

// AssignRequest is the POST /v1/admin/users/assign body.
//
// The target identity is named EITHER by user_id (an existing users.id in the
// origin/home tenant — required for local users so we can derive the synthetic
// key) OR is the actor themselves (self-assign) when self_assign=true.
//
// tenant_id is the DESTINATION tenant the membership is being created in.
type AssignRequest struct {
	// UserID is the origin/home-tenant users.id of the target identity. For
	// self-assign this is ignored (the actor's JWT subject is used).
	UserID string `json:"user_id,omitempty"`
	// TenantID is the destination tenant for the membership.
	TenantID string `json:"tenant_id"`
	// Roles are the slice-035 canonical roles to grant in the destination
	// tenant. At least one role is required.
	Roles []string `json:"roles"`
	// SelfAssign, when true, assigns the ACTOR (JWT subject) to TenantID
	// instead of UserID. Bounded to what the super_admin flag already
	// authorizes (slice 478 D4 — no new global power).
	SelfAssign bool `json:"self_assign,omitempty"`
}

// RevokeRequest is the POST /v1/admin/users/revoke body.
type RevokeRequest struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
	// RemoveMembership, when true, additionally soft-disables the membership
	// row (status='disabled'); default false keeps the role-less membership
	// (slice 478 D7).
	RemoveMembership bool `json:"remove_membership,omitempty"`
}

// AssignResponse is the result of a successful assign.
type AssignResponse struct {
	UserID            string   `json:"user_id"`
	TenantID          string   `json:"tenant_id"`
	Roles             []string `json:"roles"`
	IDPIssuer         string   `json:"idp_issuer"`
	IDPSubject        string   `json:"idp_subject"`
	MembershipCreated bool     `json:"membership_created"`
}

// CrossTenantUserResponse is one row of the super-admin cross-tenant user
// list (AC-1). Unlike the slice-062 within-tenant UserResponse it carries the
// tenant_id so a super-admin can see which tenant each membership belongs to.
type CrossTenantUserResponse struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	IDPIssuer   string   `json:"idp_issuer"`
	IDPSubject  string   `json:"idp_subject"`
	Roles       []string `json:"roles"`
}

// CrossTenantListResponse is the super-admin GET /v1/admin/users response.
type CrossTenantListResponse struct {
	Items      []CrossTenantUserResponse `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

// ListDispatch handles GET /v1/admin/users (slice 478 AC-1).
//
// Super-admin → cross-tenant paginated list (every tenant's memberships) via
// the BYPASSRLS authPool. Otherwise → the slice-062 within-tenant List
// (RLS-scoped to the caller's session tenant). The dispatch preserves the
// slice-062 behaviour exactly for non-super-admins (P0-478-3: no widening of
// a tenant-admin's view).
func (h *Handler) ListDispatch(w http.ResponseWriter, r *http.Request) {
	if isSuperAdmin(r.Context()) {
		h.listCrossTenant(w, r)
		return
	}
	h.List(w, r)
}

// listCrossTenant lists users across ALL tenants for a super-admin. Bounded
// by limit (default 50, max 200) with a keyset cursor on (tenant_id, id) so
// the scan is paginated, never unbounded (AC-1 / threat-model D).
func (h *Handler) listCrossTenant(w http.ResponseWriter, r *http.Request) {
	if h.authPool == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "cross-tenant user list unavailable: no auth pool")
		return
	}

	limit := crossTenantListLimit(r.URL.Query().Get("limit"))

	// Keyset cursor: base64("<tenant_uuid>:<user_uuid>"). Empty → first page.
	cursorTenant, cursorUser, cerr := decodeCrossTenantCursor(r.URL.Query().Get("cursor"))
	if cerr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	rows, err := h.authPool.Query(r.Context(),
		`SELECT u.id, u.tenant_id, u.email, u.display_name, u.status,
		        u.idp_issuer, u.idp_subject,
		        COALESCE(
		            (SELECT array_agg(ur.role ORDER BY ur.role)
		               FROM user_roles ur
		              WHERE ur.tenant_id = u.tenant_id
		                AND ur.user_id   = u.id::text),
		            ARRAY[]::text[]
		        )::text[] AS roles
		   FROM users u
		  WHERE ($1::uuid IS NULL)
		     OR (u.tenant_id, u.id) > ($1::uuid, $2::uuid)
		  ORDER BY u.tenant_id ASC, u.id ASC
		  LIMIT $3`,
		nullableUUID(cursorTenant), nullableUUID(cursorUser), int32(limit+1),
	)
	if err != nil {
		httperr.WriteInternal(w, r, "cross-tenant list", err)
		return
	}
	defer rows.Close()

	items := make([]CrossTenantUserResponse, 0, limit)
	for rows.Next() {
		var (
			id, tenantID               uuid.UUID
			email, displayName, status string
			idpIssuer, idpSubject      string
			roles                      []string
		)
		if serr := rows.Scan(&id, &tenantID, &email, &displayName, &status, &idpIssuer, &idpSubject, &roles); serr != nil {
			httperr.WriteInternal(w, r, "scan cross-tenant user", serr)
			return
		}
		if roles == nil {
			roles = []string{}
		}
		items = append(items, CrossTenantUserResponse{
			ID:          id.String(),
			TenantID:    tenantID.String(),
			Email:       email,
			DisplayName: displayName,
			Status:      status,
			IDPIssuer:   idpIssuer,
			IDPSubject:  idpSubject,
			Roles:       roles,
		})
	}
	if rerr := rows.Err(); rerr != nil {
		httperr.WriteInternal(w, r, "iterate cross-tenant users", rerr)
		return
	}

	var nextCursor string
	if len(items) > limit {
		last := items[limit-1]
		nextCursor = encodeCrossTenantCursor(last.TenantID, last.ID)
		items = items[:limit]
	}
	httpresp.WriteJSON(w, http.StatusOK, CrossTenantListResponse{Items: items, NextCursor: nextCursor})
}

// --- assign ---

// Assign handles POST /v1/admin/users/assign.
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	var req AssignRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAssignBody)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	destTenant, roles, verr := validateAssign(req)
	if verr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, verr.Error())
		return
	}

	actorID := actorFromContext(r.Context())
	sessionTenant, sterr := sessionTenantFromContext(r.Context())
	if sterr != nil {
		httperr.WriteInternal(w, r, "adminusers assign", sterr)
		return
	}

	isSuper := isSuperAdmin(r.Context())
	crossTenant := destTenant != sessionTenant

	// AUTHORITY GATE (P0-478-1 / AC-6). Cross-tenant requires super_admin.
	// Within-tenant requires tenant-admin (cred.IsAdmin). A non-super-admin
	// naming a foreign tenant is denied — they cannot grant beyond their
	// own authority.
	if crossTenant {
		if !isSuper {
			httpresp.WriteError(w, http.StatusForbidden, "cross-tenant assignment requires super_admin")
			return
		}
	} else {
		if !isSuper && !requireAdmin(w, r) {
			return // requireAdmin already wrote 401/403
		}
	}

	// Determine the target's origin user_id.
	var originUserID uuid.UUID
	if req.SelfAssign {
		if actorID == uuid.Nil {
			httpresp.WriteError(w, http.StatusInternalServerError, "actor user_id not on context")
			return
		}
		originUserID = actorID
	} else {
		id, err := uuid.Parse(strings.TrimSpace(req.UserID))
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "user_id must be a UUID")
			return
		}
		originUserID = id
	}

	// Cross-tenant writes need the BYPASSRLS pool (target tenant != GUC).
	if crossTenant && h.authPool == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "user assignment unavailable: no auth pool")
		return
	}

	res, err := h.doAssign(r.Context(), assignParams{
		originUserID:  originUserID,
		destTenant:    destTenant,
		roles:         roles,
		actorID:       actorID,
		sessionTenant: sessionTenant,
		crossTenant:   crossTenant,
		isSuper:       isSuper,
	})
	if err != nil {
		if errors.Is(err, errOriginNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "target user identity not found")
			return
		}
		httperr.WriteInternal(w, r, "assign user", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, res)
}

var errOriginNotFound = errors.New("origin user not found")

type assignParams struct {
	originUserID  uuid.UUID
	destTenant    uuid.UUID
	roles         []string
	actorID       uuid.UUID
	sessionTenant uuid.UUID
	crossTenant   bool
	isSuper       bool
}

// doAssign performs the atomic, idempotent membership + role write.
//
// Pool selection: cross-tenant uses the BYPASSRLS authPool; within-tenant
// uses the RLS-bound pool with the session-tenant GUC applied. Either way the
// whole operation is one transaction.
func (h *Handler) doAssign(ctx context.Context, p assignParams) (AssignResponse, error) {
	pool := h.pool
	useAuthPool := p.crossTenant
	if useAuthPool {
		pool = h.authPool
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AssignResponse{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Within-tenant writes run under RLS — apply the session-tenant GUC so
	// the four-policy RLS on users + user_roles admits the write.
	if !useAuthPool {
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			return AssignResponse{}, fmt.Errorf("apply tenant: %w", err)
		}
	}

	// 1. Read the origin row to get the target's identity (idp tuple + email
	//    + display_name). The authPool read is unscoped (no RLS); the
	//    RLS-pool read is scoped to the session tenant (the origin row IS in
	//    the session tenant for the within-tenant path).
	var idpIssuer, idpSubject, email, displayName string
	err = tx.QueryRow(ctx,
		`SELECT idp_issuer, idp_subject, email, display_name FROM users WHERE id = $1`,
		p.originUserID,
	).Scan(&idpIssuer, &idpSubject, &email, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssignResponse{}, errOriginNotFound
	}
	if err != nil {
		return AssignResponse{}, fmt.Errorf("read origin user: %w", err)
	}

	// 2. LOCAL-AUTH KEY (slice 478 D1). If the origin identity has an empty
	//    IdP tuple, mint a synthetic stable per-user key and backfill the
	//    origin row so the resolver surfaces multi-tenant memberships from
	//    ANY session. The synthetic subject is the origin user_id — unique
	//    per local user, so it can never over-match another local user.
	if idpIssuer == "" && idpSubject == "" {
		idpIssuer = LocalSyntheticIssuer
		idpSubject = p.originUserID.String()
		// Backfill the origin row (idempotent: only when still empty). The
		// origin row may live in a different tenant than dest; under the
		// authPool this is fine (no RLS). Under the RLS pool the origin row
		// is the session tenant (== dest for within-tenant) so the UPDATE is
		// admitted.
		if _, err := tx.Exec(ctx,
			`UPDATE users
			    SET idp_issuer = $2, idp_subject = $3, updated_at = now()
			  WHERE id = $1 AND idp_issuer = '' AND idp_subject = ''`,
			p.originUserID, idpIssuer, idpSubject,
		); err != nil {
			return AssignResponse{}, fmt.Errorf("backfill origin synthetic identity: %w", err)
		}
	}

	// 3. Upsert the destination-tenant membership row. Idempotent: if a row
	//    for this principal already exists in the dest tenant, reuse it (and
	//    re-activate it if it was disabled). The synthetic/real IdP tuple is
	//    the principal key.
	var destUserID uuid.UUID
	membershipCreated := false
	err = tx.QueryRow(ctx,
		`SELECT id FROM users
		  WHERE tenant_id = $1 AND idp_issuer = $2 AND idp_subject = $3`,
		p.destTenant, idpIssuer, idpSubject,
	).Scan(&destUserID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		destUserID = uuid.New()
		if _, ierr := tx.Exec(ctx,
			`INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
			 VALUES ($1, $2, $3, $4, 'active', $5, $6)`,
			destUserID, p.destTenant, email, displayName, idpIssuer, idpSubject,
		); ierr != nil {
			return AssignResponse{}, fmt.Errorf("insert dest membership: %w", ierr)
		}
		membershipCreated = true
	case err != nil:
		return AssignResponse{}, fmt.Errorf("lookup dest membership: %w", err)
	default:
		// Re-activate a previously soft-disabled membership on re-assign.
		if _, uerr := tx.Exec(ctx,
			`UPDATE users SET status = 'active', updated_at = now()
			  WHERE id = $1 AND status <> 'active'`,
			destUserID,
		); uerr != nil {
			return AssignResponse{}, fmt.Errorf("reactivate dest membership: %w", uerr)
		}
	}

	// 4. Grant the roles (idempotent on the composite PK).
	for _, role := range p.roles {
		if _, rerr := tx.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (tenant_id, user_id, role) DO NOTHING`,
			p.destTenant, destUserID.String(), role, assignRoleGrantedBy,
		); rerr != nil {
			return AssignResponse{}, fmt.Errorf("grant role %s: %w", role, rerr)
		}
	}

	// 5. Audit (AC-7). Always write me_audit_log (tenant-scoped to the
	//    actor's session tenant). On the cross-tenant super_admin path also
	//    write the platform-global super_admin_audit_log row.
	if err := h.writeAssignAudit(ctx, tx, auditAssignParams{
		action:        auditActionAssign,
		actorID:       p.actorID,
		sessionTenant: p.sessionTenant,
		destTenant:    p.destTenant,
		destUserID:    destUserID,
		roles:         p.roles,
		isSuper:       p.isSuper,
		crossTenant:   p.crossTenant,
		useAuthPool:   useAuthPool,
	}); err != nil {
		return AssignResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AssignResponse{}, fmt.Errorf("commit: %w", err)
	}

	// External sink fanout (slice 126), best-effort, post-commit.
	h.emitAssignSink(ctx, auditActionAssign, p, destUserID)

	return AssignResponse{
		UserID:            destUserID.String(),
		TenantID:          p.destTenant.String(),
		Roles:             p.roles,
		IDPIssuer:         idpIssuer,
		IDPSubject:        idpSubject,
		MembershipCreated: membershipCreated,
	}, nil
}

// --- revoke ---

// Revoke handles POST /v1/admin/users/revoke.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	var req RevokeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAssignBody)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	destTenant, err := uuid.Parse(strings.TrimSpace(req.TenantID))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "tenant_id must be a UUID")
		return
	}
	targetUserID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "user_id must be a UUID")
		return
	}

	actorID := actorFromContext(r.Context())
	sessionTenant, sterr := sessionTenantFromContext(r.Context())
	if sterr != nil {
		httperr.WriteInternal(w, r, "adminusers revoke", sterr)
		return
	}
	isSuper := isSuperAdmin(r.Context())
	crossTenant := destTenant != sessionTenant

	if crossTenant {
		if !isSuper {
			httpresp.WriteError(w, http.StatusForbidden, "cross-tenant revoke requires super_admin")
			return
		}
		if h.authPool == nil {
			httpresp.WriteError(w, http.StatusServiceUnavailable, "user assignment unavailable: no auth pool")
			return
		}
	} else {
		if !isSuper && !requireAdmin(w, r) {
			return
		}
	}

	pool := h.pool
	useAuthPool := crossTenant
	if useAuthPool {
		pool = h.authPool
	}

	tx, err := pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		httperr.WriteInternal(w, r, "begin tx", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if !useAuthPool {
		if aerr := tenancy.ApplyTenant(r.Context(), tx); aerr != nil {
			httperr.WriteInternal(w, r, "apply tenant", aerr)
			return
		}
	}

	if _, derr := tx.Exec(r.Context(),
		`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`,
		destTenant, targetUserID.String(),
	); derr != nil {
		httperr.WriteInternal(w, r, "delete roles", derr)
		return
	}

	if req.RemoveMembership {
		if _, uerr := tx.Exec(r.Context(),
			`UPDATE users SET status = 'disabled', updated_at = now()
			  WHERE id = $1 AND tenant_id = $2`,
			targetUserID, destTenant,
		); uerr != nil {
			httperr.WriteInternal(w, r, "disable membership", uerr)
			return
		}
	}

	if aerr := h.writeAssignAudit(r.Context(), tx, auditAssignParams{
		action:        auditActionRevoke,
		actorID:       actorID,
		sessionTenant: sessionTenant,
		destTenant:    destTenant,
		destUserID:    targetUserID,
		roles:         nil,
		isSuper:       isSuper,
		crossTenant:   crossTenant,
		useAuthPool:   useAuthPool,
	}); aerr != nil {
		httperr.WriteInternal(w, r, "revoke audit", aerr)
		return
	}

	if cerr := tx.Commit(r.Context()); cerr != nil {
		httperr.WriteInternal(w, r, "commit", cerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- audit helpers ---

type auditAssignParams struct {
	action        string
	actorID       uuid.UUID
	sessionTenant uuid.UUID
	destTenant    uuid.UUID
	destUserID    uuid.UUID
	roles         []string
	isSuper       bool
	crossTenant   bool
	useAuthPool   bool
}

// writeAssignAudit writes the audit rows in the same transaction as the
// membership/role write (P0: audit is non-optional, AC-7).
//
//   - me_audit_log: always (tenant-scoped to the actor's session tenant).
//     Under the authPool (no GUC) the row is inserted with an explicit
//     tenant_id; under the RLS pool the GUC is already the session tenant.
//   - super_admin_audit_log: only on the cross-tenant super_admin path
//     (platform-global forensic anchor). A within-tenant tenant-admin is NOT
//     a super_admin, so writing a super_admin_audit_log row would
//     mis-attribute platform-global authority.
func (h *Handler) writeAssignAudit(ctx context.Context, tx pgx.Tx, p auditAssignParams) error {
	afterBlob := mustMarshalJSON(map[string]any{
		"target_user_id": p.destUserID.String(),
		"dest_tenant_id": p.destTenant.String(),
		"roles":          p.roles,
		"cross_tenant":   p.crossTenant,
	})
	beforeBlob := mustMarshalJSON(map[string]any{})

	// me_audit_log — explicit tenant_id keeps it correct under BOTH pools.
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, before, after, subject_module)
		 VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, 'core')`,
		p.sessionTenant, p.actorID, p.action, beforeBlob, afterBlob,
	); err != nil {
		return fmt.Errorf("insert me_audit_log: %w", err)
	}

	if p.crossTenant && p.isSuper {
		payload := mustMarshalJSON(map[string]any{
			"dest_tenant_id": p.destTenant.String(),
			"roles":          p.roles,
		})
		if _, err := tx.Exec(ctx,
			`INSERT INTO super_admin_audit_log
			 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
			 VALUES ($1, $2, $3, $4, $5)`,
			p.action, p.destUserID, p.actorID, p.sessionTenant, payload,
		); err != nil {
			return fmt.Errorf("insert super_admin_audit_log: %w", err)
		}
	}
	return nil
}

func (h *Handler) emitAssignSink(ctx context.Context, action string, p assignParams, destUserID uuid.UUID) {
	payload := mustMarshalJSON(map[string]any{
		"dest_tenant_id": p.destTenant.String(),
		"roles":          p.roles,
	})
	sink.EmitDefault(ctx, unifiedlog.Entry{
		OccurredAt:    time.Now().UTC(),
		ActorID:       p.actorID.String(),
		TenantID:      p.sessionTenant,
		Kind:          unifiedlog.KindMe,
		TargetType:    "user_tenant",
		TargetID:      destUserID.String(),
		Action:        action,
		RowID:         uuid.New(),
		SubjectModule: unifiedlog.SubjectModuleCore,
		PayloadJSON:   payload,
	})
}

// --- validation (pure-Go, fast unit-testable: slice-353 Q-2) ---

// validateAssign validates the assign request body and returns the parsed
// destination tenant + the de-duplicated canonical role set. Errors are
// safe to surface to the client (no internal detail).
func validateAssign(req AssignRequest) (uuid.UUID, []string, error) {
	destTenant, err := uuid.Parse(strings.TrimSpace(req.TenantID))
	if err != nil {
		return uuid.Nil, nil, errors.New("tenant_id must be a UUID")
	}
	if !req.SelfAssign && strings.TrimSpace(req.UserID) == "" {
		return uuid.Nil, nil, errors.New("user_id is required unless self_assign=true")
	}
	if len(req.Roles) == 0 {
		return uuid.Nil, nil, errors.New("at least one role is required")
	}
	seen := make(map[string]struct{}, len(req.Roles))
	out := make([]string, 0, len(req.Roles))
	for _, raw := range req.Roles {
		role := strings.TrimSpace(raw)
		if !authz.IsCanonical(authz.Role(role)) {
			return uuid.Nil, nil, errors.New("unknown role: " + role)
		}
		if _, dup := seen[role]; dup {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	return destTenant, out, nil
}

// --- context helpers ---

// isSuperAdmin reports whether the caller carries the verified super_admin
// JWT claim. Mirrors adminsuperadmins.requireSuperAdmin's load-bearing check.
func isSuperAdmin(ctx context.Context) bool {
	claims := jwtmw.FromContext(ctx)
	return claims != nil && claims.SuperAdmin
}

// actorFromContext returns the caller's user_id. Prefers the JWT subject
// (super_admin is JWT-only at v1); falls back to the slice-062 credential
// UserID for the within-tenant tenant-admin path that may run on the legacy
// bearer credential.
func actorFromContext(ctx context.Context) uuid.UUID {
	if claims := jwtmw.FromContext(ctx); claims != nil {
		// The atlas JWT Subject is "user:<uuid>" (auth-substrate-v2
		// convention); strip the prefix before parsing or every real-auth
		// caller resolves to uuid.Nil.
		if u, err := uuid.Parse(jwtmw.SubjectUserID(claims.Subject)); err == nil {
			return u
		}
	}
	if cred, ok := credentialFromContext(ctx); ok {
		if u, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID)); err == nil {
			return u
		}
	}
	return uuid.Nil
}

// sessionTenantFromContext resolves the caller's session tenant UUID.
func sessionTenantFromContext(ctx context.Context) (uuid.UUID, error) {
	str, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, errors.New("tenant context missing")
	}
	id, perr := uuid.Parse(str)
	if perr != nil {
		return uuid.Nil, errors.New("invalid tenant in context")
	}
	return id, nil
}

func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("adminusers: json marshal: %v", err))
	}
	return b
}

// --- cross-tenant list pagination helpers (pure-Go, unit-testable) ---

// crossTenantListLimit parses the limit query param, clamping to
// [1, maxListLimit] with defaultListLimit as the fallback.
func crossTenantListLimit(raw string) int {
	if raw == "" {
		return defaultListLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}

// encodeCrossTenantCursor produces an opaque keyset cursor over
// (tenant_id, user_id). Both inputs are uuid string forms.
func encodeCrossTenantCursor(tenantID, userID string) string {
	return base64.URLEncoding.EncodeToString([]byte(tenantID + ":" + userID))
}

// decodeCrossTenantCursor parses the opaque cursor back into (tenant, user)
// UUIDs. An empty cursor yields the zero UUIDs (caller treats as first page).
func decodeCrossTenantCursor(raw string) (uuid.UUID, uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, uuid.Nil, nil
	}
	dec, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("cursor not base64")
	}
	parts := strings.SplitN(string(dec), ":", 2)
	if len(parts) != 2 {
		return uuid.Nil, uuid.Nil, errors.New("cursor malformed")
	}
	t, terr := uuid.Parse(parts[0])
	if terr != nil {
		return uuid.Nil, uuid.Nil, errors.New("cursor tenant invalid")
	}
	u, uerr := uuid.Parse(parts[1])
	if uerr != nil {
		return uuid.Nil, uuid.Nil, errors.New("cursor user invalid")
	}
	return t, u, nil
}

// nullableUUID returns nil for the zero UUID (so the SQL `$1::uuid IS NULL`
// first-page branch fires) and the *uuid otherwise.
func nullableUUID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}
