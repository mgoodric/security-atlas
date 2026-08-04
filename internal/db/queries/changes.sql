-- Change-management register queries for OE-629.

-- name: CreateChange :one
INSERT INTO changes (
    id, tenant_id, title, description,
    source, source_ref, source_url,
    proposed_by, risk_notes, rollback_notes
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    $8, $9, $10
)
RETURNING *;

-- name: GetChangeByID :one
SELECT *
FROM changes
WHERE tenant_id = $1 AND id = $2;

-- name: GetChangeBySourceRef :one
SELECT *
FROM changes
WHERE tenant_id = $1 AND source = $2 AND source_ref = $3;

-- name: ListChanges :many
SELECT *
FROM changes
WHERE tenant_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit');

-- name: ApproveChange :one
UPDATE changes
SET status = 'approved',
    approver_id = $3,
    approved_at = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'proposed'
RETURNING *;

-- name: ImplementChange :one
UPDATE changes
SET status = 'implemented',
    implemented_by = $3,
    implemented_at = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'approved'
RETURNING *;

-- name: VerifyChange :one
UPDATE changes
SET status = 'verified',
    verified_by = $3,
    verified_at = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'implemented'
RETURNING *;

-- name: LinkChangeControl :exec
INSERT INTO change_controls (change_id, control_id, tenant_id, linked_by)
VALUES ($1, $2, $3, $4);

-- name: ListChangeControls :many
SELECT control_id, linked_at, linked_by
FROM change_controls
WHERE tenant_id = $1 AND change_id = $2
ORDER BY linked_at ASC, control_id ASC;

-- name: ChangeControlExistsInTenant :one
SELECT EXISTS (
    SELECT 1 FROM controls WHERE tenant_id = $1 AND id = $2
);

-- name: ChangeUserExistsInTenant :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE tenant_id = $1 AND id = $2
);

-- name: WriteChangeAuditLog :one
INSERT INTO change_audit_log (
    id, tenant_id, change_id, actor_id, action_type, before_state, after_state
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListChangeAuditLog :many
SELECT *
FROM change_audit_log
WHERE tenant_id = $1 AND change_id = $2
ORDER BY created_at ASC, id ASC;

-- name: ChangeRollup :one
SELECT
    count(*)::bigint AS total,
    count(*) FILTER (WHERE status = 'proposed')::bigint AS proposed,
    count(*) FILTER (WHERE status = 'approved')::bigint AS approved,
    count(*) FILTER (WHERE status = 'implemented')::bigint AS implemented,
    count(*) FILTER (WHERE status = 'verified')::bigint AS verified,
    count(*) FILTER (WHERE status <> 'verified')::bigint AS backlog,
    count(*) FILTER (WHERE verified_at >= now() - interval '30 days')::bigint AS verified_last_30_days
FROM changes
WHERE tenant_id = $1;
