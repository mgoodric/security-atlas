-- name: UpsertFramework :one
-- Insert or update a framework row. The (tenant_id, slug) UNIQUE constraint
-- in slice 002's schema treats NULLs as distinct, so a partial unique index
-- on slug-when-tenant-is-null would be needed to catch global-catalog dupes
-- via the natural key. To avoid a follow-on migration, the importer uses a
-- deterministic id derived from the slug; ON CONFLICT (id) DO UPDATE then
-- handles re-imports cleanly.
INSERT INTO frameworks (id, tenant_id, name, slug, issuer, description, latest_version_id)
VALUES ($1, NULL, $2, $3, $4, $5, NULL)
ON CONFLICT (id) DO UPDATE
SET name        = EXCLUDED.name,
    issuer      = EXCLUDED.issuer,
    description = EXCLUDED.description
RETURNING *;

-- name: UpsertFrameworkVersion :one
-- Insert or update a framework_versions row. Same deterministic-id pattern
-- as UpsertFramework above (avoids the NULLs-distinct gotcha on natural-key
-- ON CONFLICT targets).
INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, effective_to, status, requirement_count, oscal_catalog_uri)
VALUES ($1, NULL, $2, $3, $4, $5, $6, 0, NULL)
ON CONFLICT (id) DO UPDATE
SET status         = EXCLUDED.status,
    effective_from = EXCLUDED.effective_from,
    effective_to   = EXCLUDED.effective_to
RETURNING *;

-- name: DemoteCurrentFrameworkVersions :exec
-- Flip every "current" framework_version for the given framework to "legacy"
-- so a new release can take over without violating the at-most-one-current
-- invariant. Caller scopes the transaction.
UPDATE framework_versions
SET status = 'legacy'
WHERE framework_id = $1 AND status = 'current';

-- name: SetLatestVersion :exec
-- Point a framework at its current version.
UPDATE frameworks
SET latest_version_id = $2
WHERE id = $1;

-- name: ListFrameworks :many
SELECT * FROM frameworks
WHERE tenant_id IS NULL
ORDER BY slug;

-- name: ListFrameworkVersionsBySlug :many
SELECT fv.*
FROM framework_versions fv
JOIN frameworks f ON f.id = fv.framework_id
WHERE f.slug = $1 AND fv.tenant_id IS NULL
ORDER BY fv.effective_from DESC NULLS LAST, fv.version DESC;

-- name: UpsertSCFAnchor :one
-- Idempotent insert keyed on (framework_version_id, scf_id) — both columns
-- are NOT NULL so the NULLs-distinct gotcha doesn't apply here. Returns
-- the resulting row plus an indication of whether the row was an insert
-- (xmax = 0 on insert, non-zero on update).
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description, subtopics)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (framework_version_id, scf_id) DO UPDATE
SET family      = EXCLUDED.family,
    title       = EXCLUDED.title,
    description = EXCLUDED.description,
    subtopics   = EXCLUDED.subtopics,
    updated_at  = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: ListSCFAnchorsForVersion :many
-- Paginated anchor list for a specific framework_version. Caller supplies
-- limit + offset; default at the call site.
SELECT *
FROM scf_anchors
WHERE framework_version_id = $1
ORDER BY scf_id
LIMIT $2 OFFSET $3;

-- name: CountSCFAnchorsForVersion :one
SELECT count(*) FROM scf_anchors WHERE framework_version_id = $1;

-- name: ListSCFAnchorsLatest :many
-- Paginated anchor list for the latest current SCF framework_version.
SELECT a.*
FROM scf_anchors a
JOIN framework_versions fv ON fv.id = a.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
ORDER BY a.scf_id
LIMIT $1 OFFSET $2;

-- name: GetSCFAnchorByID :one
SELECT * FROM scf_anchors WHERE id = $1;

-- name: GetSCFAnchorBySCFID :one
-- Look up an anchor by its SCF code (e.g., "IAC-06") in the current SCF
-- framework version.
SELECT a.*
FROM scf_anchors a
JOIN framework_versions fv ON fv.id = a.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
WHERE f.slug = 'scf' AND fv.status = 'current' AND a.scf_id = $1 AND f.tenant_id IS NULL;
