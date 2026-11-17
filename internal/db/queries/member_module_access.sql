-- #2172: module access for each member. Every query runs under
-- tenancy.ApplyTenant as atlas_app; the explicit tenant_id predicate is the
-- primary guarantee and RLS the second leg.

-- name: GetMemberModuleAccess :one
SELECT tenant_id, user_id, modules, updated_by, updated_at
FROM member_module_access
WHERE tenant_id = $1 AND user_id = $2;

-- name: GetMemberModuleAccessForUpdate :one
-- The current row, locked, so the audit row's before is the value the
-- upsert replaces.
SELECT tenant_id, user_id, modules, updated_by, updated_at
FROM member_module_access
WHERE tenant_id = $1 AND user_id = $2
FOR UPDATE;

-- name: ListMemberModuleAccess :many
SELECT tenant_id, user_id, modules, updated_by, updated_at
FROM member_module_access
WHERE tenant_id = $1
ORDER BY user_id;

-- name: UpsertMemberModuleAccess :exec
INSERT INTO member_module_access (tenant_id, user_id, modules, updated_by, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET modules = EXCLUDED.modules,
    updated_by = EXCLUDED.updated_by,
    updated_at = now();

-- name: InsertMemberModuleAccessAudit :exec
INSERT INTO member_module_access_audit_log (tenant_id, user_id, action, before, after, actor)
VALUES ($1, $2, 'set', $3, $4, $5);
