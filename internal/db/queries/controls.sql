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

-- name: ListActiveControlsForExport :many
-- Slice 137 — controls UCF graph data-export projection.
--
-- Returns every active (non-superseded) control for the caller's
-- tenant with the canonical export column set (slice 137 D2), capped
-- at $1 rows. The caller passes (row_cap + 1) so the handler can
-- detect the row-cap-exceeded path with no extra round trip.
--
-- Column projection rationale (see docs/audit-log/137-controls-ucf-
-- export-decisions.md D2):
--
--   identity:    id, bundle_id, version, title, control_family
--   topology:    scf_id, scf_anchor_id (foreign-key join columns;
--                downstream consumers reconstruct the UCF graph
--                against the public SCF catalog + fw_to_scf_edges)
--   posture:     implementation_type, owner_role, lifecycle_state
--   tenant data: applicability_expr (the slice 017 DSL — RLS protects
--                tenant isolation, NOT column omission)
--   integrity:   freshness_class, bundle_manifest_hash
--   audit:       created_at, updated_at
--
-- RLS posture: the WHERE tenant_id = $1 clause is belt-and-suspenders
-- alongside the GUC-driven RLS policy (slice 002). The tenancy.ApplyTenant
-- call upstream pins the GUC; the explicit WHERE protects against any
-- future RLS-policy regression. The existing ListActiveControls query
-- carries the same belt-and-suspenders clause.
SELECT id, bundle_id, version, scf_id, scf_anchor_id, title,
       control_family, implementation_type, owner_role, lifecycle_state,
       applicability_expr, freshness_class, bundle_manifest_hash,
       created_at, updated_at
FROM controls
WHERE tenant_id = $1 AND superseded_by IS NULL
ORDER BY bundle_id ASC, version DESC
LIMIT $2;

-- name: ListControlsHistoryForExport :many
-- Slice 175 — control bundle history export projection (lineage incl. superseded).
--
-- Returns EVERY control version for the caller's tenant — active rows
-- AND superseded rows — with the slice 137 column projection PLUS two
-- new columns (`superseded_by`, `superseded_at`). Capped at $2 rows.
-- The caller passes (row_cap + 1) so the handler can detect the
-- row-cap-exceeded path with no extra round trip.
--
-- Why a SEPARATE query (slice 175 D2):
--
--   - Slice 137 D2 explicitly rejected including `superseded_by` /
--     `superseded_at` in the active-only export because those columns
--     would always be NULL. Extending the slice 137 query would
--     re-introduce that "always-NULL noise" against the active-only
--     stream — wrong shape for both consumers. Two queries keep both
--     projections clean.
--   - Active-only export consumers (compliance gap analysis, auditor
--     handoff index sheets) MUST keep seeing the slice 137 shape
--     unchanged. Reshaping that query for both consumers would force a
--     downstream-tool migration that buys nothing.
--
-- Column projection rationale (slice 175 acceptance criterion AC-2 —
-- 17 columns; the slice 137 15 columns IN THE SAME ORDER plus two new
-- columns appended):
--
--   identity:     id, bundle_id, version, title, control_family
--   topology:     scf_id, scf_anchor_id (foreign-key join columns)
--   posture:      implementation_type, owner_role, lifecycle_state
--   tenant data:  applicability_expr
--   integrity:    freshness_class, bundle_manifest_hash
--   audit:        created_at, updated_at
--   supersession: superseded_by, superseded_at  (slice 175 NEW)
--
-- `superseded_at` is NOT a stored column on controls; the slice 175
-- handler synthesises it from `updated_at` ONLY for rows whose
-- `superseded_by IS NOT NULL`. Rationale: the supersession transaction
-- (MarkControlSuperseded) sets `superseded_by` and bumps `updated_at =
-- now()` in the same UPDATE, so for superseded rows `updated_at` is
-- the timestamp of the supersession event. Adding a dedicated stored
-- column would be a separate schema slice; the handler-level synthesis
-- gets us the AC-2 column at zero schema cost. The SQL projection
-- returns `superseded_by` and `updated_at` separately; the handler
-- emits an empty `superseded_at` cell when `superseded_by IS NULL`.
--
-- Ordering (slice 175 narrative §1): `bundle_id ASC, version DESC` so
-- consumers see the most-recent-first lineage per bundle.
--
-- RLS posture: identical to slice 137. The WHERE tenant_id = $1 clause
-- is belt-and-suspenders alongside the GUC-driven RLS policy; the
-- tenancy.ApplyTenant call upstream pins the GUC.
SELECT id, bundle_id, version, scf_id, scf_anchor_id, title,
       control_family, implementation_type, owner_role, lifecycle_state,
       applicability_expr, freshness_class, bundle_manifest_hash,
       created_at, updated_at, superseded_by
FROM controls
WHERE tenant_id = $1
ORDER BY bundle_id ASC, version DESC
LIMIT $2;

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
