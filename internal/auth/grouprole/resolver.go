package grouprole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrInvalidSource is returned when Derive is called with an unrecognized
// source. A defensive guard — callers should always pass SourceOIDC/SourceSCIM.
var ErrInvalidSource = errors.New("grouprole: invalid derivation source")

// DeriveInput is the validated identity + group set fed to the resolver. The
// caller (a verified-JWT claim reader or an authenticated SCIM-group handler)
// MUST have validated `Groups` already — the resolver never accepts raw,
// unvalidated input (P0-509-2). The resolver only maps + reconciles.
type DeriveInput struct {
	// UserID is the affected atlas user's id (the user_roles.user_id text form).
	UserID string
	// IDPConfigID scopes the lookup. For the OIDC-claim path it is the specific
	// oidc_idp_configs row the login used (multi-IdP independence, AC-6). For
	// the SCIM path it is uuid.Nil → matches the NULL-source (SCIM) mappings.
	IDPConfigID uuid.UUID
	// Groups is the VALIDATED set of IdP group identifiers (ids or names) the
	// identity belongs to. Unmapped groups contribute nothing (fail-closed,
	// P0-509-1). Empty is valid: it revokes all of the user's group-derived
	// roles (subject to the last-admin guard).
	Groups []string
	// Source is the derivation channel, recorded in the audit log.
	Source Source
}

// DeriveResult reports what the reconciliation did (for callers + tests).
type DeriveResult struct {
	Granted []string // roles newly granted as group-derived
	Revoked []string // group-derived roles removed
	// SuppressedRevokes are group-derived roles whose revoke was blocked by the
	// last-admin guard (AC-5 / P0-509-3); they remain granted.
	SuppressedRevokes []string
	// ResolvedRoles is the full target role set the mappings produced for the
	// validated group set (the union, de-duplicated).
	ResolvedRoles []string
}

// Resolver derives + reconciles group-mapped roles. It is the ONE resolver both
// the OIDC and SCIM sources call (AC-2), so the mapping logic cannot diverge.
type Resolver struct {
	pool *pgxpool.Pool
}

// NewResolver constructs a Resolver over the RLS app pool.
func NewResolver(pool *pgxpool.Pool) *Resolver { return &Resolver{pool: pool} }

// Derive maps the validated group set to roles via the tenant's mappings and
// reconciles the user's group-derived role rows in one transaction. Manual
// roles are never touched (AC-4); the last-admin guard holds (AC-5); every
// grant/revoke writes an append-only audit row (AC-7). The tenant comes from
// the RLS context (ctx), never from input.
func (r *Resolver) Derive(ctx context.Context, in DeriveInput) (DeriveResult, error) {
	if !in.Source.Valid() {
		return DeriveResult{}, ErrInvalidSource
	}
	if in.UserID == "" {
		return DeriveResult{}, errors.New("grouprole: user_id required")
	}

	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return DeriveResult{}, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return DeriveResult{}, fmt.Errorf("grouprole: parse tenant id: %w", err)
	}
	tID := pgtype.UUID{Bytes: tenantID, Valid: true}
	idpCfg := nullableUUID(in.IDPConfigID)

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeriveResult{}, fmt.Errorf("grouprole: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return DeriveResult{}, err
	}
	q := dbx.New(tx)

	// 1. Resolve the target role set + remember the triggering group per role
	//    (for the audit row). Only groups present in the mapping table produce
	//    a row — unmapped groups contribute nothing (P0-509-1).
	target := make(map[string]struct{})
	triggeringGroup := make(map[string]string)
	if len(in.Groups) > 0 {
		rows, rErr := q.ResolveRolesForGroups(ctx, dbx.ResolveRolesForGroupsParams{
			TenantID:    tID,
			IdpConfigID: idpCfg,
			Groups:      in.Groups,
		})
		if rErr != nil {
			return DeriveResult{}, fmt.Errorf("grouprole: resolve roles: %w", rErr)
		}
		for _, row := range rows {
			target[row.Role] = struct{}{}
			// First group that grants a role is recorded as the trigger; stable
			// because ResolveRolesForGroups orders deterministically via DISTINCT.
			if _, seen := triggeringGroup[row.Role]; !seen {
				triggeringGroup[row.Role] = row.GroupRef
			}
		}
	}

	// 2. Load the user's current group-derived roles.
	currentRoles, err := q.ListGroupDerivedRoles(ctx, dbx.ListGroupDerivedRolesParams{
		TenantID: tID, UserID: in.UserID,
	})
	if err != nil {
		return DeriveResult{}, fmt.Errorf("grouprole: list current derived: %w", err)
	}
	current := setOf(currentRoles)

	// 3. Tenant admin count + whether THIS user holds admin manually — inputs to
	//    the last-admin guard (AC-5).
	adminCount, err := q.CountTenantAdmins(ctx, tID)
	if err != nil {
		return DeriveResult{}, fmt.Errorf("grouprole: count admins: %w", err)
	}
	userHoldsManualAdmin, err := q.HasManualRole(ctx, dbx.HasManualRoleParams{
		TenantID: tID, UserID: in.UserID, Role: roleAdmin,
	})
	if err != nil {
		return DeriveResult{}, fmt.Errorf("grouprole: check manual admin: %w", err)
	}

	// 4. Compute the pure plan.
	plan := planReconcile(reconcileState{
		target:               target,
		current:              current,
		tenantAdminCount:     int(adminCount),
		userHoldsManualAdmin: userHoldsManualAdmin,
	})

	// 5. Apply grants.
	for _, role := range plan.grants {
		if err := q.InsertGroupDerivedRole(ctx, dbx.InsertGroupDerivedRoleParams{
			TenantID:  tID,
			UserID:    in.UserID,
			Role:      role,
			GrantedBy: "group:" + string(in.Source),
		}); err != nil {
			return DeriveResult{}, fmt.Errorf("grouprole: grant %s: %w", role, err)
		}
		if err := r.audit(ctx, q, tID, in, role, "grant", triggeringGroup[role]); err != nil {
			return DeriveResult{}, err
		}
	}

	// 6. Apply revokes. The DELETE is origin='group-derived'-scoped, so a manual
	//    row with the same (tenant, user, role) is never removed (AC-4).
	for _, role := range plan.revokes {
		if _, err := q.DeleteGroupDerivedRole(ctx, dbx.DeleteGroupDerivedRoleParams{
			TenantID: tID, UserID: in.UserID, Role: role,
		}); err != nil {
			return DeriveResult{}, fmt.Errorf("grouprole: revoke %s: %w", role, err)
		}
		if err := r.audit(ctx, q, tID, in, role, "revoke", ""); err != nil {
			return DeriveResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return DeriveResult{}, fmt.Errorf("grouprole: commit: %w", err)
	}

	return DeriveResult{
		Granted:           plan.grants,
		Revoked:           plan.revokes,
		SuppressedRevokes: plan.suppressedRevokes,
		ResolvedRoles:     sortedKeys(target),
	}, nil
}

// audit appends one group_role_audit_log row for a grant/revoke (AC-7).
func (r *Resolver) audit(ctx context.Context, q *dbx.Queries, tID pgtype.UUID, in DeriveInput, role, change, group string) error {
	detail, _ := json.Marshal(map[string]any{
		"resolved_via_group": group,
	})
	if err := q.InsertGroupRoleAudit(ctx, dbx.InsertGroupRoleAuditParams{
		TenantID:        tID,
		UserID:          in.UserID,
		Role:            role,
		Change:          change,
		Source:          string(in.Source),
		IdpConfigID:     nullableUUID(in.IDPConfigID),
		TriggeringGroup: group,
		Detail:          detail,
	}); err != nil {
		return fmt.Errorf("grouprole: audit %s %s: %w", change, role, err)
	}
	return nil
}

// ValidateMappingRole rejects a mapping that targets a non-existent atlas role
// (P0-509-4). The DB CHECK is the backstop; this is the clean-400 gate the CRUD
// handler calls before the INSERT.
func ValidateMappingRole(role string) error {
	if !authz.IsCanonical(authz.Role(role)) {
		return fmt.Errorf("grouprole: unknown role %q (mappings may only target an existing atlas role)", role)
	}
	return nil
}

// --- helpers ---

func nullableUUID(u uuid.UUID) pgtype.UUID {
	if u == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small set; insertion of canonical roles — keep deterministic.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
