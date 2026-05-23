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

-- name: GetSCFAnchorByVersionAndSCFID :one
-- Existing-row lookup. Returns ErrNoRows when the anchor doesn't exist yet.
-- The importer calls this first to classify the upsert as Created /
-- Updated / Unchanged (xmax-based detection inside ON CONFLICT can't
-- distinguish "updated to the same content" from "actually updated").
SELECT * FROM scf_anchors
WHERE framework_version_id = $1 AND scf_id = $2;

-- name: InsertSCFAnchor :one
-- Insert a fresh anchor (use after GetSCFAnchorByVersionAndSCFID returned
-- ErrNoRows). Uniqueness is enforced by (framework_version_id, scf_id).
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description, subtopics)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateSCFAnchor :one
-- Update an existing anchor in place. Touches updated_at; the caller
-- decides whether to call this based on a content-equality check.
UPDATE scf_anchors
SET family      = $2,
    title       = $3,
    description = $4,
    subtopics   = $5,
    updated_at  = now()
WHERE id = $1
RETURNING *;

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

-- name: ListSCFAnchorsLatestWithState :many
-- Slice 104: paginated anchor list for the latest current SCF
-- framework_version, LEFT JOINed to the worst-state-wins per-anchor
-- rollup over the tenant's `control_evaluations` ledger.
--
-- Slice 224: extended to accept an optional `scope_cell_id` filter.
-- When the parameter is the NULL UUID sentinel (`pgtype.UUID{Valid:false}`),
-- the per-cell predicate inside `worst_per_anchor` is a no-op and every
-- evaluation row participates in the rollup. When the parameter is a
-- valid UUID, the rollup narrows to only the evaluations recorded against
-- that scope cell — i.e. "what's the per-anchor state in just THIS cell?".
-- Out-of-tenant cell ids return zero rows naturally via the existing
-- tenant RLS on `control_evaluations` (no 404 leak).
--
-- Shape:
--   1. `latest_eval` CTE picks the latest control_evaluations row per
--      (tenant, control_id, scope_cell_id) via DISTINCT ON — the same
--      pattern slice 012's ListLatestControlEvaluations uses for one
--      control. This honors slice 012's append-only ledger semantics
--      (we never lose history; we pick the current state).
--   2. `worst_per_anchor` aggregates across every cell of every
--      tenant-instantiated control that satisfies one anchor:
--        result rank: fail (4) > inconclusive (3) > pass (2) > na (1)
--        freshness  : expired (4) > stale (3) > no_evidence (2) > fresh (1)
--   3. Outer SELECT joins scf_anchors LEFT JOIN worst_per_anchor — an
--      anchor with no tenant control returns NULLs for every state
--      column (the handler renders `state: null`).
--
-- Constitutional invariants:
--   #6 RLS: `controls` and `control_evaluations` are tenant-scoped under
--      FORCE ROW LEVEL SECURITY. The tenant GUC set by `tenancymw`
--      filters those rows; the global `scf_anchors` rows
--      (`tenant_id IS NULL`) are visible to every tenant by design.
--   #2 Engine is sole writer of control_evaluations: this is a pure read
--      over the engine's output table, never a parallel computation.
--   #1 One control, N framework satisfactions: state is rolled up per
--      ANCHOR (the catalog spine node), not per framework.
--
-- NOTE: the matching Go method lives in internal/db/dbx/scf_anchors.sql.go
-- (hand-maintained to keep the rest of the dbx tree HEAD-blessed per the
-- regen-on-rebase note in MEMORY.md). Keep the two in sync.
WITH latest_eval AS (
    SELECT DISTINCT ON (ce.tenant_id, ce.control_id, ce.scope_cell_id)
        ce.tenant_id,
        ce.control_id,
        ce.scope_cell_id,
        ce.result,
        ce.freshness_status,
        ce.last_observed_at,
        ce.evaluated_at
    FROM control_evaluations ce
    ORDER BY ce.tenant_id, ce.control_id, ce.scope_cell_id, ce.evaluated_at DESC, ce.created_at DESC
),
worst_per_anchor AS (
    SELECT
        c.scf_anchor_id AS anchor_id,
        CASE MAX(CASE le.result
                    WHEN 'fail'         THEN 4
                    WHEN 'inconclusive' THEN 3
                    WHEN 'pass'         THEN 2
                    WHEN 'na'           THEN 1
                    ELSE 0
                 END)
            WHEN 4 THEN 'fail'
            WHEN 3 THEN 'inconclusive'
            WHEN 2 THEN 'pass'
            WHEN 1 THEN 'na'
        END::evidence_result AS result,
        (CASE MAX(CASE le.freshness_status
                    WHEN 'expired'     THEN 4
                    WHEN 'stale'       THEN 3
                    WHEN 'no_evidence' THEN 2
                    WHEN 'fresh'       THEN 1
                    ELSE 0
                 END)
            WHEN 4 THEN 'expired'
            WHEN 3 THEN 'stale'
            WHEN 2 THEN 'no_evidence'
            WHEN 1 THEN 'fresh'
        END)::text AS freshness_status,
        MAX(le.last_observed_at) AS last_observed_at,
        MAX(le.evaluated_at)     AS evaluated_at
    FROM controls c
    JOIN latest_eval le ON le.tenant_id = c.tenant_id AND le.control_id = c.id
    WHERE c.superseded_by IS NULL
      AND c.scf_anchor_id IS NOT NULL
      -- Slice 224: optional scope cell filter. When $3 is the NULL UUID
      -- sentinel, this predicate is a no-op (every evaluation participates).
      -- When $3 is a valid uuid, the rollup narrows to that cell.
      AND ($3::uuid IS NULL OR le.scope_cell_id = $3::uuid)
    GROUP BY c.scf_anchor_id
)
SELECT
    a.id, a.framework_version_id, a.scf_id, a.family, a.title,
    a.description, a.subtopics, a.created_at, a.updated_at,
    -- Slice 159 Option C: drop the explicit `::evidence_result` /
    -- `::text` casts on the outer SELECT so sqlc v1.31.1 sees the
    -- CTE's nullable-via-LEFT-JOIN typing flow through naturally.
    -- The CTE's `result` column is already typed `evidence_result`
    -- via the `END::evidence_result` inside `worst_per_anchor`; the
    -- LEFT JOIN to `worst_per_anchor` makes both columns nullable in
    -- the row shape. With the redundant outer casts, sqlc treated
    -- the columns as non-nullable; without them, sqlc emits
    -- `*EvidenceResult` / `*string` (under
    -- `emit_pointers_for_null_types: true`). The slice-104 handler
    -- adapters switch to nil-check + dereference (pointer-style)
    -- instead of the `.Valid` / `.EvidenceResult` / `.String` wrapper
    -- API. JSON response shape is unchanged. See
    -- `docs/audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md`.
    wpa.result                        AS state_result,
    wpa.freshness_status              AS state_freshness_status,
    wpa.last_observed_at::timestamptz AS state_last_observed_at,
    wpa.evaluated_at::timestamptz     AS state_evaluated_at
FROM scf_anchors a
JOIN framework_versions fv ON fv.id = a.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
LEFT JOIN worst_per_anchor wpa ON wpa.anchor_id = a.id
WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
ORDER BY a.scf_id
LIMIT $1 OFFSET $2;

-- name: ListSCFAnchorsForVersionWithState :many
-- Slice 104: version-scoped sibling to ListSCFAnchorsLatestWithState.
-- Same CTE shape; the only difference is the WHERE clause filters
-- scf_anchors to the caller-supplied framework_version_id instead of
-- the current SCF version. Kept as two queries (rather than one with
-- a NULL sentinel) so the planner can inline the simpler predicate
-- and so the parameter types stay tight for sqlc codegen.
--
-- Slice 224: extended to accept an optional `scope_cell_id` filter
-- on the same NULL-UUID sentinel pattern as the latest-with-state
-- variant above.
WITH latest_eval AS (
    SELECT DISTINCT ON (ce.tenant_id, ce.control_id, ce.scope_cell_id)
        ce.tenant_id,
        ce.control_id,
        ce.scope_cell_id,
        ce.result,
        ce.freshness_status,
        ce.last_observed_at,
        ce.evaluated_at
    FROM control_evaluations ce
    ORDER BY ce.tenant_id, ce.control_id, ce.scope_cell_id, ce.evaluated_at DESC, ce.created_at DESC
),
worst_per_anchor AS (
    SELECT
        c.scf_anchor_id AS anchor_id,
        CASE MAX(CASE le.result
                    WHEN 'fail'         THEN 4
                    WHEN 'inconclusive' THEN 3
                    WHEN 'pass'         THEN 2
                    WHEN 'na'           THEN 1
                    ELSE 0
                 END)
            WHEN 4 THEN 'fail'
            WHEN 3 THEN 'inconclusive'
            WHEN 2 THEN 'pass'
            WHEN 1 THEN 'na'
        END::evidence_result AS result,
        (CASE MAX(CASE le.freshness_status
                    WHEN 'expired'     THEN 4
                    WHEN 'stale'       THEN 3
                    WHEN 'no_evidence' THEN 2
                    WHEN 'fresh'       THEN 1
                    ELSE 0
                 END)
            WHEN 4 THEN 'expired'
            WHEN 3 THEN 'stale'
            WHEN 2 THEN 'no_evidence'
            WHEN 1 THEN 'fresh'
        END)::text AS freshness_status,
        MAX(le.last_observed_at) AS last_observed_at,
        MAX(le.evaluated_at)     AS evaluated_at
    FROM controls c
    JOIN latest_eval le ON le.tenant_id = c.tenant_id AND le.control_id = c.id
    WHERE c.superseded_by IS NULL
      AND c.scf_anchor_id IS NOT NULL
      -- Slice 224: optional scope cell filter; see comment on the
      -- latest-with-state variant above.
      AND ($4::uuid IS NULL OR le.scope_cell_id = $4::uuid)
    GROUP BY c.scf_anchor_id
)
SELECT
    a.id, a.framework_version_id, a.scf_id, a.family, a.title,
    a.description, a.subtopics, a.created_at, a.updated_at,
    -- Slice 159 Option C: drop the explicit `::evidence_result` /
    -- `::text` casts on the outer SELECT so sqlc v1.31.1 sees the
    -- CTE's nullable-via-LEFT-JOIN typing flow through naturally.
    -- The CTE's `result` column is already typed `evidence_result`
    -- via the `END::evidence_result` inside `worst_per_anchor`; the
    -- LEFT JOIN to `worst_per_anchor` makes both columns nullable in
    -- the row shape. With the redundant outer casts, sqlc treated
    -- the columns as non-nullable; without them, sqlc emits
    -- `*EvidenceResult` / `*string` (under
    -- `emit_pointers_for_null_types: true`). The slice-104 handler
    -- adapters switch to nil-check + dereference (pointer-style)
    -- instead of the `.Valid` / `.EvidenceResult` / `.String` wrapper
    -- API. JSON response shape is unchanged. See
    -- `docs/audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md`.
    wpa.result                        AS state_result,
    wpa.freshness_status              AS state_freshness_status,
    wpa.last_observed_at::timestamptz AS state_last_observed_at,
    wpa.evaluated_at::timestamptz     AS state_evaluated_at
FROM scf_anchors a
LEFT JOIN worst_per_anchor wpa ON wpa.anchor_id = a.id
WHERE a.framework_version_id = $1
ORDER BY a.scf_id
LIMIT $2 OFFSET $3;

-- name: ListAllSCFAnchorsForExport :many
-- Slice 174: anchor catalog export. Returns EVERY SCF anchor in the
-- current SCF framework_version (status='current'), joined to
-- framework_versions + frameworks so the export carries the framework
-- provenance (`framework_slug`, `framework_version`) without a
-- second query. Sorted by `scf_id` for stable output across runs and
-- across tenants (the global catalog is identical for every tenant,
-- so the bit-for-bit-identical cross-tenant assertion in the
-- integration test relies on this stable ordering).
--
-- Catalog data — no tenant_id; no RLS clause; bounded result set
-- (~1,400 anchors at current SCF release). The `LIMIT` parameter
-- carries the slice 174 row cap so the handler can detect cap-
-- exceeded by asking for cap+1.
SELECT
    a.id,
    a.scf_id,
    a.family,
    a.title,
    a.description,
    a.subtopics,
    a.created_at,
    a.updated_at,
    fv.id           AS framework_version_id,
    fv.version      AS framework_version,
    f.slug          AS framework_slug
FROM scf_anchors a
JOIN framework_versions fv ON fv.id = a.framework_version_id
JOIN frameworks f          ON f.id = fv.framework_id
WHERE f.slug = 'scf'
  AND fv.status = 'current'
  AND f.tenant_id IS NULL
ORDER BY a.scf_id
LIMIT $1;

-- name: ListAllFwToScfEdgesForExport :many
-- Slice 174: anchor catalog export edges projection. Returns EVERY
-- fw_to_scf_edges row whose target anchor is in the current SCF
-- framework_version, joined to framework_requirements +
-- framework_versions + frameworks so consumers get the natural-key
-- (`framework_slug:version:code`) without a second query.
--
-- Catalog data — no tenant_id; no RLS clause; bounded result set
-- (~10K edges at current SCF release: ~1,400 anchors × 3-8 edges
-- per anchor average). `no_relationship` edges are INCLUDED here
-- because the export is the canonical catalog dump — operators
-- doing reconciliation want to see "this anchor explicitly does NOT
-- map to that requirement" as a recorded fact, not a silent
-- omission. The slice 008 traversal views filter no_relationship
-- out for coverage UI; the export does NOT.
--
-- Sorted by anchor_scf_id then framework_slug + framework_version
-- + requirement_code so the rows group by anchor (matches the
-- visual ordering of the JSON projection's nested array).
SELECT
    e.id                          AS edge_id,
    e.scf_anchor_id               AS anchor_id,
    a.scf_id                      AS anchor_scf_id,
    e.framework_requirement_id    AS framework_requirement_id,
    r.code                        AS framework_requirement_code,
    r.title                       AS framework_requirement_title,
    f.slug                        AS framework_slug,
    fv.version                    AS framework_version,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.rationale
FROM fw_to_scf_edges e
JOIN scf_anchors a                ON a.id = e.scf_anchor_id
JOIN framework_versions afv       ON afv.id = a.framework_version_id
JOIN frameworks af                ON af.id = afv.framework_id
JOIN framework_requirements r     ON r.id = e.framework_requirement_id
JOIN framework_versions fv        ON fv.id = r.framework_version_id
JOIN frameworks f                 ON f.id = fv.framework_id
WHERE af.slug = 'scf'
  AND afv.status = 'current'
  AND af.tenant_id IS NULL
ORDER BY a.scf_id, f.slug, fv.version, r.code;
