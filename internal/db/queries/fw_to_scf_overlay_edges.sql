-- #1797: tenant overlay edges (create, list, retire). Every query runs under
-- the caller's tenant (RLS); tenant_id is also passed explicitly as a second
-- leg, the house style for tenant tables.

-- name: InsertOverlayEdge :one
INSERT INTO fw_to_scf_overlay_edges (
    tenant_id, framework_requirement_id, scf_anchor_id,
    relationship_type, strength, rationale, source_ref, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: LockOverlayEdge :one
SELECT * FROM fw_to_scf_overlay_edges
WHERE tenant_id = $1 AND id = $2
FOR UPDATE;

-- name: RetireOverlayEdge :one
UPDATE fw_to_scf_overlay_edges
SET status            = 'retired',
    retirement_reason = sqlc.arg(reason)::text,
    retired_by        = sqlc.arg(actor)::text,
    retired_at        = now()
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id) AND status = 'active'
RETURNING *;

-- name: ListOverlayEdges :many
-- The tenant's overlay edges with the codes they were named by. Each filter
-- is optional: framework slug, requirement code, anchor SCF code, status.
SELECT
    e.*,
    f.slug       AS framework_slug,
    fv.version   AS framework_version,
    fr.code      AS requirement_code,
    a.scf_id     AS anchor_code,
    av.version   AS scf_release
FROM fw_to_scf_overlay_edges e
JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
JOIN framework_versions fv ON fv.id = fr.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
JOIN scf_anchors a ON a.id = e.scf_anchor_id
JOIN framework_versions av ON av.id = a.framework_version_id
WHERE e.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(framework_slug)::text IS NULL OR f.slug = sqlc.narg(framework_slug)::text)
  AND (sqlc.narg(requirement_code)::text IS NULL OR fr.code = sqlc.narg(requirement_code)::text)
  AND (sqlc.narg(anchor_code)::text IS NULL OR a.scf_id = sqlc.narg(anchor_code)::text)
  AND (sqlc.narg(status)::text IS NULL OR e.status = sqlc.narg(status)::text)
ORDER BY e.created_at DESC, e.id;

-- name: GetOverlayEdgeView :one
-- One overlay edge with the same joined codes ListOverlayEdges returns.
SELECT
    e.*,
    f.slug       AS framework_slug,
    fv.version   AS framework_version,
    fr.code      AS requirement_code,
    a.scf_id     AS anchor_code,
    av.version   AS scf_release
FROM fw_to_scf_overlay_edges e
JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
JOIN framework_versions fv ON fv.id = fr.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
JOIN scf_anchors a ON a.id = e.scf_anchor_id
JOIN framework_versions av ON av.id = a.framework_version_id
WHERE e.tenant_id = $1 AND e.id = $2;

-- name: InsertOverlayEdgeAuditLog :exec
INSERT INTO fw_to_scf_overlay_edge_audit_log (
    tenant_id, overlay_edge_id, action, before, after, actor
) VALUES (
    $1, $2, $3, $4, $5, $6
);

-- name: ListOverlayEdgeAuditLog :many
SELECT * FROM fw_to_scf_overlay_edge_audit_log
WHERE tenant_id = $1 AND overlay_edge_id = $2
ORDER BY occurred_at, id;
