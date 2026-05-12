-- Slice 009: control bundle queries. The active version per (tenant_id,
-- bundle_id) is the row with superseded_by IS NULL (partial unique index
-- enforces uniqueness). Re-upload supersedes by first UPDATE-ing the prior
-- row's superseded_by, then INSERTing the new row — both in the same tx.

-- name: GetActiveControlByBundleID :one
SELECT id, tenant_id, bundle_id, version, superseded_by, scf_id, scf_anchor_id,
       title, description, control_family, implementation_type,
       owner_role, lifecycle_state, applicability_expr,
       evidence_queries, manual_evidence_schema, linked_policy_ids,
       freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
       bundle_uploaded_at, bundle_uploaded_by, created_at, updated_at
FROM controls
WHERE tenant_id = $1 AND bundle_id = $2 AND superseded_by IS NULL;

-- name: GetControlByID :one
SELECT id, tenant_id, bundle_id, version, superseded_by, scf_id, scf_anchor_id,
       title, description, control_family, implementation_type,
       owner_role, lifecycle_state, applicability_expr,
       evidence_queries, manual_evidence_schema, linked_policy_ids,
       freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
       bundle_uploaded_at, bundle_uploaded_by, created_at, updated_at
FROM controls
WHERE tenant_id = $1 AND id = $2;

-- name: ListControlVersionsByBundle :many
-- Newest first. Includes superseded rows so callers see the supersession chain.
SELECT id, tenant_id, bundle_id, version, superseded_by, scf_id, scf_anchor_id,
       title, lifecycle_state, bundle_manifest_hash, created_at
FROM controls
WHERE tenant_id = $1 AND bundle_id = $2
ORDER BY version DESC;

-- name: ListActiveControls :many
-- Every active (non-superseded) control for the active tenant.
SELECT id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title,
       control_family, implementation_type, owner_role, lifecycle_state,
       applicability_expr, freshness_class, bundle_manifest_hash, created_at
FROM controls
WHERE tenant_id = $1 AND superseded_by IS NULL
ORDER BY bundle_id ASC;

-- name: InsertControlVersion :one
-- Insert a new control row (initial upload or supersession). Caller is
-- responsible for UPDATE-ing the predecessor's superseded_by in the same tx.
INSERT INTO controls (
    id, tenant_id, bundle_id, version, superseded_by,
    scf_id, scf_anchor_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    evidence_queries, manual_evidence_schema, linked_policy_ids,
    freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
    bundle_uploaded_at, bundle_uploaded_by
) VALUES (
    $1, $2, $3, $4, NULL,
    $5, $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16,
    $17, $18, $19,
    now(), $20
)
RETURNING *;

-- name: MarkControlSuperseded :exec
-- Flip a predecessor row to superseded. Idempotent: no-op if already set.
UPDATE controls
SET superseded_by = $3, updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND superseded_by IS NULL;

-- NOTE: SCF anchor lookups reuse GetSCFAnchorByID and GetSCFAnchorBySCFID
-- from internal/db/queries/scf_anchors.sql (slice 006). The bundle parser
-- in internal/control/parser.go calls them through the same dbx.Queries
-- facade.
