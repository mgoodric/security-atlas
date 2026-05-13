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
