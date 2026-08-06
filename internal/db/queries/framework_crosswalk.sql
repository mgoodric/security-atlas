-- name: UpsertFrameworkRequirement :one
-- Insert or update a framework_requirements row. Deterministic id derived
-- from (framework_version_id, code) so re-imports are idempotent without
-- relying on NULLs-distinct gotchas on the UNIQUE constraint.
INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
SET title       = EXCLUDED.title,
    body        = EXCLUDED.body,
    updated_at  = now()
RETURNING *;

-- name: GetFrameworkRequirementByID :one
SELECT * FROM framework_requirements WHERE id = $1;

-- name: GetFrameworkRequirementByVersionAndCode :one
-- Lookup by natural key. Used by the importer to classify
-- Created/Updated/Unchanged and by the traversal handler for the
-- code-form path (e.g., resolve "CC6.6" against the current SOC 2 version).
SELECT * FROM framework_requirements
WHERE framework_version_id = $1 AND code = $2;

-- name: GetFrameworkRequirementByFrameworkSlugVersionCode :one
-- Resolve a colon-delimited requirement id like "soc2:2017:CC6.6" by
-- joining the framework + framework_version. tenant_id IS NULL constraint
-- restricts to the global catalog.
SELECT fr.*
FROM framework_requirements fr
JOIN framework_versions fv ON fv.id = fr.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
WHERE f.slug = $1 AND fv.version = $2 AND fr.code = $3 AND f.tenant_id IS NULL;

-- name: GetFrameworkRequirementByCurrentVersion :one
-- Same as above but uses the framework's "current" version. Convenience
-- query so callers can omit the version (e.g., "soc2::CC6.6").
SELECT fr.*
FROM framework_requirements fr
JOIN framework_versions fv ON fv.id = fr.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
WHERE f.slug = $1 AND fv.status = 'current' AND fr.code = $2 AND f.tenant_id IS NULL;

-- name: ListFrameworkRequirementsForVersion :many
SELECT *
FROM framework_requirements
WHERE framework_version_id = $1
ORDER BY code;

-- name: CountFrameworkRequirementsForVersion :one
SELECT count(*) FROM framework_requirements WHERE framework_version_id = $1;

-- name: UpdateFrameworkVersionRequirementCount :exec
-- Tally — the importer keeps framework_versions.requirement_count in sync
-- so dashboards can show "60 controls" without a count(*).
UPDATE framework_versions
SET requirement_count = $2
WHERE id = $1;

-- name: GetFwToScfEdge :one
-- Look up one edge by (requirement, anchor). Returns ErrNoRows when the
-- edge doesn't exist yet. Importer calls this first to classify
-- Created/Updated/Unchanged.
SELECT * FROM fw_to_scf_edges
WHERE framework_requirement_id = $1 AND scf_anchor_id = $2;

-- name: InsertFwToScfEdge :one
INSERT INTO fw_to_scf_edges (
    id, framework_requirement_id, scf_anchor_id,
    relationship_type, strength, source_attribution, rationale
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateFwToScfEdge :one
-- Update an existing edge in place. Importer decides whether to call this
-- based on a content-equality check.
UPDATE fw_to_scf_edges
SET relationship_type    = $2,
    strength             = $3,
    source_attribution   = $4,
    rationale            = $5,
    updated_at           = now()
WHERE id = $1
RETURNING *;

-- name: ListFwToScfEdgesForRequirement :many
-- Reverse traversal — given a requirement, return all SCF anchors it maps
-- to with relationship type and strength. Joins through scf_anchors so the
-- caller gets the scf_id + family + title in one round trip.
SELECT
    e.id,
    e.framework_requirement_id,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale,
    a.scf_id,
    a.family,
    a.title AS anchor_title
FROM fw_to_scf_edges e
JOIN scf_anchors a ON a.id = e.scf_anchor_id
WHERE e.framework_requirement_id = $1
ORDER BY e.strength DESC, a.scf_id;

-- name: CountFwToScfEdgesBySourceAttribution :one
-- Audit query — exposed for integration tests + the audit log.
SELECT count(*) FROM fw_to_scf_edges WHERE source_attribution = $1;

-- ===== slice 483: crosswalk mapping-tier governance =====

-- name: GetFwToScfEdgeTier :one
-- Read the current trust tier of one edge by id. The transition store calls
-- this FOR UPDATE (see GetFwToScfEdgeTierForUpdate) inside the tx; this plain
-- variant is for read-only callers. Returns ErrNoRows for an unknown edge.
SELECT id, mapping_tier, source_attribution
FROM fw_to_scf_edges
WHERE id = $1;

-- name: GetFwToScfEdgeTierForUpdate :one
-- Row-lock the edge's tier inside the transition transaction so a concurrent
-- transition cannot race the read-validate-write window. Returns ErrNoRows for
-- an unknown edge (the handler maps that to 404).
SELECT id, mapping_tier, source_attribution
FROM fw_to_scf_edges
WHERE id = $1
FOR UPDATE;

-- name: SetFwToScfEdgeTier :exec
-- Flip ONLY the trust tier (the narrow column-level UPDATE grant — slice 483
-- D1). Legality of the move is enforced in Go (internal/crosswalktier state
-- machine) BEFORE this runs; this query is the unconditional write inside the
-- same tx that also inserts the audit row.
UPDATE fw_to_scf_edges
SET mapping_tier = $2,
    updated_at   = now()
WHERE id = $1;

-- name: InsertFwToScfEdgeTierTransition :one
-- Append the immutable audit row for a tier transition (threat-model R /
-- P0-483-4). Written in the SAME transaction as SetFwToScfEdgeTier.
INSERT INTO fw_to_scf_edge_tier_transitions (
    edge_id, reviewer_id, from_tier, to_tier, note
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListFwToScfEdgeTierTransitions :many
-- Admin/maintainer-scoped read of an edge's transition history (newest first).
-- NOT on the public /anchors payload — reviewer identity stays behind the admin
-- boundary (threat-model I / P0-483-6).
SELECT *
FROM fw_to_scf_edge_tier_transitions
WHERE edge_id = $1
ORDER BY created_at DESC;

-- ===== slice 536b-1: crosswalk review surface + content editing =====

-- name: ListFwToScfEdgesForFrameworkVersion :many
-- The review-queue listing: every STRM edge under one framework version,
-- joined through framework_requirements (for the code the reviewer reads) and
-- scf_anchors (for the family-scoped conflict heuristics). Bounded by the
-- framework_version_id predicate (slice 536 threat-model D); ordered by
-- requirement code then anchor scf_id so the queue is stable between calls.
SELECT
    e.id,
    e.framework_requirement_id,
    fr.code  AS requirement_code,
    fr.title AS requirement_title,
    e.scf_anchor_id,
    a.scf_id,
    a.family,
    a.title  AS anchor_title,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale
FROM fw_to_scf_edges e
JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
JOIN scf_anchors a ON a.id = e.scf_anchor_id
WHERE fr.framework_version_id = $1
ORDER BY fr.code, a.scf_id, e.id;

-- name: GetFwToScfEdgeContentForUpdate :one
-- Row-lock the edge's content + tier inside the edit transaction so a
-- concurrent edit or tier transition cannot race the read-validate-write
-- window. Returns ErrNoRows for an unknown edge (the handler maps to 404).
SELECT id, relationship_type, strength, rationale, mapping_tier
FROM fw_to_scf_edges
WHERE id = $1
FOR UPDATE;

-- name: UpdateFwToScfEdgeContent :exec
-- Rewrite ONLY the reviewer-curated content columns (the D-536b-2 widened
-- column-level grant — migration 20260804000000). source_attribution and the
-- edge-endpoint FKs are not writable through atlas_app's grants; tier
-- editability is enforced in Go (internal/crosswalkedit) BEFORE this runs;
-- this query is the unconditional write inside the same tx that also inserts
-- the content-edit audit row.
UPDATE fw_to_scf_edges
SET relationship_type = $2,
    strength          = $3,
    rationale         = $4,
    updated_at        = now()
WHERE id = $1;

-- name: InsertFwToScfEdgeContentEdit :one
-- Append the immutable before/after audit row for a content edit
-- (threat-model R — the content twin of InsertFwToScfEdgeTierTransition).
-- Written in the SAME transaction as UpdateFwToScfEdgeContent.
INSERT INTO fw_to_scf_edge_content_edits (
    edge_id, editor_id,
    from_relationship_type, to_relationship_type,
    from_strength, to_strength,
    from_rationale, to_rationale,
    note
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListFwToScfEdgeContentEdits :many
-- Admin/maintainer-scoped read of an edge's content-edit history (newest
-- first). Editor identity stays behind the admin boundary, mirroring the
-- tier-transition trail (threat-model I / P0-483-6).
SELECT *
FROM fw_to_scf_edge_content_edits
WHERE edge_id = $1
ORDER BY created_at DESC;
