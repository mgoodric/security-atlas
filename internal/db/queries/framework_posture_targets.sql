-- #2136: posture targets per framework version. Every query runs under
-- tenancy.ApplyTenant as atlas_app; the explicit tenant_id predicate is the
-- primary guarantee and RLS the second leg.

-- name: ListFrameworkPostureTargets :many
SELECT tenant_id, framework_version_id, target_value, warning_threshold,
       critical_threshold, direction, updated_by, created_at, updated_at
FROM framework_posture_targets
WHERE tenant_id = $1
ORDER BY framework_version_id;

-- name: GetFrameworkPostureTargetForUpdate :one
-- The current row, locked, so the before half of the audit row is the
-- state the upsert replaces.
SELECT tenant_id, framework_version_id, target_value, warning_threshold,
       critical_threshold, direction, updated_by, created_at, updated_at
FROM framework_posture_targets
WHERE tenant_id = $1 AND framework_version_id = $2
FOR UPDATE;

-- name: GetFrameworkPostureTarget :one
SELECT tenant_id, framework_version_id, target_value, warning_threshold,
       critical_threshold, direction, updated_by, created_at, updated_at
FROM framework_posture_targets
WHERE tenant_id = $1 AND framework_version_id = $2;

-- name: FrameworkVersionScopeState :one
-- Whether the caller can see the framework version (a catalog version, or
-- one of its tenant's: framework_versions' tenant_or_catalog RLS policy
-- decides) and whether its tenant has an activated scope for it.
SELECT
    fv.id,
    EXISTS (
        SELECT 1
        FROM framework_scopes fs
        WHERE fs.tenant_id = sqlc.arg('tenant_id')
          AND fs.framework_version_id = fv.id
          AND fs.state = 'activated'
    )::bool AS scope_activated
FROM framework_versions fv
WHERE fv.id = sqlc.arg('framework_version_id')
  AND (fv.tenant_id IS NULL OR fv.tenant_id = sqlc.arg('tenant_id'));

-- name: UpsertFrameworkPostureTarget :one
INSERT INTO framework_posture_targets (
    tenant_id, framework_version_id, target_value, warning_threshold,
    critical_threshold, direction, updated_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, framework_version_id) DO UPDATE SET
    target_value       = EXCLUDED.target_value,
    warning_threshold  = EXCLUDED.warning_threshold,
    critical_threshold = EXCLUDED.critical_threshold,
    direction          = EXCLUDED.direction,
    updated_by         = EXCLUDED.updated_by,
    updated_at         = now()
RETURNING tenant_id, framework_version_id, target_value, warning_threshold,
          critical_threshold, direction, updated_by, created_at, updated_at;

-- name: InsertFrameworkPostureTargetAudit :exec
INSERT INTO framework_posture_target_audit_log (
    tenant_id, framework_version_id, action, before, after, actor
) VALUES ($1, $2, 'set', $3, $4, $5);
