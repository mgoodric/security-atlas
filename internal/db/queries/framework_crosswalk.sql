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

-- ===== slice 536b: crosswalk review queue + content editing =====

-- name: ListFrameworkRequirementsForVersionPaged :many
-- The review queue's page of requirements, ordered by code so the operator's
-- position is stable across refreshes. Paginated because slice 536's
-- threat-model D asks for a bounded scan per framework_version rather than the
-- whole catalog in one response.
SELECT *
FROM framework_requirements
WHERE framework_version_id = $1
ORDER BY code
LIMIT $2 OFFSET $3;

-- name: ListFwToScfEdgesForRequirementIDs :many
-- One round trip for a whole page of requirements' edges, instead of N calls to
-- ListFwToScfEdgesForRequirement. Same projection as that query plus the
-- requirement code, which the conflict adapter needs to label a finding and
-- which the per-requirement variant cannot supply (it joins scf_anchors, not
-- framework_requirements).
SELECT
    e.id,
    e.framework_requirement_id,
    r.code AS requirement_code,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale,
    e.updated_at,
    a.scf_id,
    a.family,
    a.title AS anchor_title
FROM fw_to_scf_edges e
JOIN scf_anchors a ON a.id = e.scf_anchor_id
JOIN framework_requirements r ON r.id = e.framework_requirement_id
WHERE e.framework_requirement_id = ANY($1::uuid[])
ORDER BY r.code, a.scf_id;

-- name: GetFwToScfEdgeContentForUpdate :one
-- Row-lock the edge's editable content inside the edit transaction so a
-- concurrent edit cannot race the read-validate-write window (the same shape
-- GetFwToScfEdgeTierForUpdate uses for the tier path). mapping_tier comes back
-- with it because a content edit on a `verified` edge demotes it in the same tx
-- (slice 536b D-536b-1). Returns ErrNoRows for an unknown edge.
SELECT id, framework_requirement_id, scf_anchor_id,
       relationship_type, strength, rationale, mapping_tier
FROM fw_to_scf_edges
WHERE id = $1
FOR UPDATE;

-- name: SetFwToScfEdgeContent :exec
-- Apply a reviewer's content edit. The statement writes ONLY the three STRM
-- content columns the slice-536b grant widened to, plus updated_at. It never
-- names framework_requirement_id, scf_anchor_id or source_attribution — and
-- atlas_app is not granted UPDATE on those columns either, so invariant #7 (an
-- edit can never re-point an edge) and the provenance axis are enforced at the
-- database as well as here. Validation runs in Go BEFORE this executes; the
-- audit row is inserted in the same tx.
UPDATE fw_to_scf_edges
SET relationship_type = $2,
    strength          = $3,
    rationale         = $4,
    updated_at        = now()
WHERE id = $1;

-- name: InsertFwToScfEdgeContentEdit :one
-- Append the immutable audit row for a content edit (threat-model R). Written
-- in the SAME transaction as SetFwToScfEdgeContent — there is no code path that
-- changes edge content without also inserting here.
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
-- first). Editor identity stays behind the admin boundary, never on the public
-- /anchors payload (the slice-483 P0-483-6 line, held for this trail too).
SELECT *
FROM fw_to_scf_edge_content_edits
WHERE edge_id = $1
ORDER BY created_at DESC;
