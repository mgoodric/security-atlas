-- Slice 509 — IdP group-to-role mapping queries.
--
-- Two clusters:
--   (1) Mapping CRUD (oidc_idp_group_mappings) — the admin control plane.
--   (2) Derivation/reconciliation (user_roles origin-aware + the
--       group_role_audit_log append) — the resolver's read/write surface.
--
-- Every query is tenant-scoped in WHERE and runs under app.current_tenant RLS
-- (invariant #6). idp_config_id is matched with IS NOT DISTINCT FROM so a NULL
-- source (SCIM) matches NULL and a non-NULL source (a specific OIDC config)
-- matches that exact config (AC-6 multi-IdP independence).

-- ===== mapping CRUD =====

-- name: InsertGroupRoleMapping :one
-- Adds a (group_ref -> role) mapping for a tenant + source. Idempotent on the
-- unique index (tenant, COALESCE(idp_config_id,nil), group_ref, role): a
-- duplicate returns the existing row. The role CHECK enforces P0-509-4 at the
-- DB layer; the application validates authz.IsCanonical first for a clean 400.
INSERT INTO oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref, role, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, COALESCE(idp_config_id, '00000000-0000-0000-0000-000000000000'::uuid), group_ref, role)
DO UPDATE SET group_ref = EXCLUDED.group_ref
RETURNING id, tenant_id, idp_config_id, group_ref, role, created_at, created_by;

-- name: GroupRoleMappingIdpConfigExists :one
-- Reports whether idp_config_id names one of the tenant's own oidc_idp_configs
-- (#2643). Runs under RLS, so another tenant's config reads as absent: a
-- foreign id and a nonexistent one are indistinguishable to the caller.
SELECT EXISTS (
    SELECT 1 FROM oidc_idp_configs
    WHERE tenant_id = $1 AND id = $2
) AS config_exists;

-- name: ListGroupRoleMappings :many
-- All mappings for a tenant, ordered for stable display. Admin CRUD list (AC-8).
SELECT id, tenant_id, idp_config_id, group_ref, role, created_at, created_by
FROM oidc_idp_group_mappings
WHERE tenant_id = $1
ORDER BY group_ref ASC, role ASC, idp_config_id ASC NULLS FIRST;

-- name: GetGroupRoleMapping :one
-- Single mapping by id within the tenant. 404 (ErrNoRows) when absent.
SELECT id, tenant_id, idp_config_id, group_ref, role, created_at, created_by
FROM oidc_idp_group_mappings
WHERE tenant_id = $1 AND id = $2;

-- name: DeleteGroupRoleMapping :execrows
-- Removes a mapping by id within the tenant. Returns affected-row count so the
-- handler can 404 a missing id.
DELETE FROM oidc_idp_group_mappings
WHERE tenant_id = $1 AND id = $2;

-- name: ResolveRolesForGroups :many
-- The resolver's core lookup: the DISTINCT set of roles a tenant's mappings
-- grant for a given source (idp_config_id) and group-set. idp_config_id is
-- matched with IS NOT DISTINCT FROM so NULL (SCIM) only matches NULL mappings
-- and a specific config only matches that config's mappings (AC-6). An unmapped
-- group contributes no row, so a user in only-unmapped groups gets an empty set
-- (P0-509-1 fail-closed). Returns the triggering group alongside the role so
-- the audit row can record WHICH group granted it (AC-7).
SELECT DISTINCT role, group_ref
FROM oidc_idp_group_mappings
WHERE tenant_id = $1
  AND idp_config_id IS NOT DISTINCT FROM sqlc.narg('idp_config_id')::uuid
  AND group_ref = ANY(sqlc.arg('groups')::text[]);

-- ===== user_roles origin-aware reconciliation =====

-- name: ListGroupDerivedRolesForSource :many
-- The user's CURRENT group-derived roles from ONE source (#2760): the roles
-- user_role_group_sources attributes to this idp_config_id (NULL = SCIM).
-- The resolver diffs this against that source's freshly-resolved target
-- set, so a reconcile through one source never revokes a role another
-- source derived. Manual roles have no source row and are never touched
-- by re-derivation (AC-4).
SELECT role
FROM user_role_group_sources
WHERE tenant_id = $1 AND user_id = $2
  AND idp_config_id IS NOT DISTINCT FROM sqlc.narg('idp_config_id')::uuid
ORDER BY role ASC;

-- name: InsertGroupRoleSource :exec
-- Records that a source derived the user's role (#2760). The user_roles row
-- must exist first (FK). Idempotent on the per-source unique index.
INSERT INTO user_role_group_sources (tenant_id, user_id, role, idp_config_id)
VALUES ($1, $2, $3, sqlc.narg('idp_config_id')::uuid)
ON CONFLICT DO NOTHING;

-- name: DeleteGroupRoleSource :execrows
-- Removes one source's derivation of the user's role (#2760).
DELETE FROM user_role_group_sources
WHERE tenant_id = $1 AND user_id = $2 AND role = $3
  AND idp_config_id IS NOT DISTINCT FROM sqlc.narg('idp_config_id')::uuid;

-- name: GroupRoleHasSource :one
-- Reports whether any source still derives the user's role (#2760). The
-- group-derived user_roles row is removed only when none does.
SELECT EXISTS (
    SELECT 1 FROM user_role_group_sources
    WHERE tenant_id = $1 AND user_id = $2 AND role = $3
) AS has_source;

-- name: GroupRoleHasOtherSource :one
-- Reports whether a source other than idp_config_id derives the user's role
-- (#2760). The last-admin guard reads it: a user another source keeps admin
-- for is not stranded by this source's revoke.
SELECT EXISTS (
    SELECT 1 FROM user_role_group_sources
    WHERE tenant_id = $1 AND user_id = $2 AND role = $3
      AND idp_config_id IS DISTINCT FROM sqlc.narg('idp_config_id')::uuid
) AS has_other_source;

-- name: ListGroupRoleSourcesForConfig :many
-- Every (user, role) one OIDC config derived, for the config delete (#2760).
-- FOR UPDATE so a concurrent reconcile cannot drop a row mid-delete.
SELECT user_id, role
FROM user_role_group_sources
WHERE tenant_id = $1 AND idp_config_id = $2
ORDER BY user_id ASC, role ASC
FOR UPDATE;

-- name: CountGroupRoleMappingsForConfig :one
-- The mappings scoped to one OIDC config: the rows deleting it removes.
SELECT count(*)::bigint AS mapping_count
FROM oidc_idp_group_mappings
WHERE tenant_id = $1 AND idp_config_id = $2;

-- name: CountSoleSourceGroupRolesForConfig :one
-- The group-derived roles deleting one OIDC config would revoke (#2760):
-- the config's source rows on a group-derived user_roles row that no other
-- source derives. A role the user also holds manually is not counted.
SELECT count(*)::bigint AS role_count
FROM user_role_group_sources s
JOIN user_roles ur
  ON ur.tenant_id = s.tenant_id AND ur.user_id = s.user_id AND ur.role = s.role
WHERE s.tenant_id = $1 AND s.idp_config_id = $2
  AND ur.origin = 'group-derived'
  AND NOT EXISTS (
      SELECT 1 FROM user_role_group_sources o
      WHERE o.tenant_id = s.tenant_id AND o.user_id = s.user_id AND o.role = s.role
        AND o.idp_config_id IS DISTINCT FROM s.idp_config_id
  );

-- name: HasManualRole :one
-- Reports whether the user holds a SPECIFIC role via a manual assignment. Used
-- by the resolver so a group-derived grant does not duplicate a manual row, and
-- so a revoke never deletes a role the user also holds manually.
SELECT EXISTS (
    SELECT 1 FROM user_roles
    WHERE tenant_id = $1 AND user_id = $2 AND role = $3 AND origin = 'manual'
) AS has_manual;

-- name: InsertGroupDerivedRole :exec
-- Grants a role to the user with origin='group-derived'. Idempotent on the
-- composite PK. granted_by records the derivation source label.
INSERT INTO user_roles (tenant_id, user_id, role, granted_by, origin)
VALUES ($1, $2, $3, $4, 'group-derived')
ON CONFLICT (tenant_id, user_id, role) DO NOTHING;

-- name: DeleteGroupDerivedRole :execrows
-- Revokes a SPECIFIC group-derived role from the user. The origin='group-derived'
-- predicate is the safety belt (AC-4): a manual row with the same (tenant, user,
-- role) is never deleted by this query. Returns the affected-row count.
DELETE FROM user_roles
WHERE tenant_id = $1 AND user_id = $2 AND role = $3 AND origin = 'group-derived';


-- ===== group-derived role audit (AC-7) =====

-- name: InsertGroupRoleAudit :exec
-- Appends one row to the append-only group_role_audit_log for every
-- group-derived grant/revoke, capturing the triggering group + source.
INSERT INTO group_role_audit_log
    (tenant_id, user_id, role, change, source, idp_config_id, triggering_group, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListGroupRoleAuditForUser :many
-- The group-derived role-change history for a user in the tenant (admin read).
SELECT id, tenant_id, occurred_at, user_id, role, change, source,
       idp_config_id, triggering_group, detail
FROM group_role_audit_log
WHERE tenant_id = $1 AND user_id = $2
ORDER BY occurred_at DESC;
