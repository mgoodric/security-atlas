-- Slice 076 — metrics catalog + cascade + observation store.
--
-- The catalog + cascade reads are platform-shared (singleton, tenant-
-- agnostic). The observation / target / input reads + writes are
-- tenant-scoped through RLS.

-- name: ListMetricsCatalog :many
-- List catalog metrics filtered by optional level + category. Pass
-- empty string ('') for "no filter" -- the WHERE clause shortcut
-- avoids null-typed parameter inference issues with sqlc v1.31.x.
SELECT *
FROM metrics_catalog
WHERE (@level::text = '' OR level = @level::text)
  AND (@category::text = '' OR category = @category::text)
ORDER BY level, category, id;

-- name: GetMetricCatalog :one
-- Fetch one catalog metric by id (the text slug). Returns ErrNoRows if
-- the id doesn't exist.
SELECT *
FROM metrics_catalog
WHERE id = $1;

-- name: ListMetricCatalogParents :many
-- Parents of a single metric (one level). Used by GET /v1/metrics/{id}
-- to render the metric's cascade context.
SELECT mc.*
FROM metrics_catalog mc
JOIN metric_cascade_edges e ON e.parent_id = mc.id
WHERE e.child_id = $1
ORDER BY mc.id;

-- name: ListMetricCatalogChildren :many
-- Children of a single metric (one level). Used by GET /v1/metrics/{id}.
SELECT mc.*
FROM metrics_catalog mc
JOIN metric_cascade_edges e ON e.child_id = mc.id
WHERE e.parent_id = $1
ORDER BY mc.id;

-- name: ListComputedCatalog :many
-- Every catalog metric whose compute_strategy is 'computed'. The 15-min
-- cron iterates this list per tenant.
SELECT *
FROM metrics_catalog
WHERE compute_strategy = 'computed';

-- name: GetMetricCascade :many
-- Recursive walk starting at every catalog metric with the supplied
-- level, descending through parent → child edges up to depth_limit
-- levels. Returns one row per (descendant, parent, depth). The API
-- handler reassembles the tree client-side; the recursive CTE caps at
-- depth_limit to prevent runaway traversal on a content bug. depth_limit
-- defaults to 3 at the call site.
WITH RECURSIVE cascade AS (
    SELECT mc.id           AS metric_id,
           NULL::text       AS parent_id,
           1                AS depth
    FROM metrics_catalog mc
    WHERE mc.level = @level::text
    UNION ALL
    SELECT child.id         AS metric_id,
           e.parent_id      AS parent_id,
           c.depth + 1      AS depth
    FROM cascade c
    JOIN metric_cascade_edges e ON e.parent_id = c.metric_id
    JOIN metrics_catalog child ON child.id = e.child_id
    WHERE c.depth < @depth_limit::int
)
SELECT metric_id, parent_id, depth
FROM cascade
ORDER BY depth, metric_id;

-- name: UpsertMetricCatalogGlobal :exec
-- Platform-seeded global catalog upsert. The seeder runs as atlas_migrate
-- (BYPASSRLS) at boot so the tenant_id NULL row is permitted.
INSERT INTO metrics_catalog (
    id, tenant_id, level, category, name, description, unit, cadence,
    compute_strategy, compute_evaluator, source_slices, notes
) VALUES (
    $1, NULL, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
ON CONFLICT (id) DO UPDATE
SET level             = EXCLUDED.level,
    category          = EXCLUDED.category,
    name              = EXCLUDED.name,
    description       = EXCLUDED.description,
    unit              = EXCLUDED.unit,
    cadence           = EXCLUDED.cadence,
    compute_strategy  = EXCLUDED.compute_strategy,
    compute_evaluator = EXCLUDED.compute_evaluator,
    source_slices     = EXCLUDED.source_slices,
    notes             = EXCLUDED.notes,
    updated_at        = now();

-- name: UpsertMetricCascadeEdge :exec
-- Platform-seeded cascade-edge upsert. Runs as atlas_migrate (BYPASSRLS).
INSERT INTO metric_cascade_edges (parent_id, child_id, weight, notes)
VALUES ($1, $2, $3, $4)
ON CONFLICT (parent_id, child_id) DO UPDATE
SET weight = EXCLUDED.weight,
    notes  = EXCLUDED.notes;

-- name: DeleteAllMetricCascadeEdges :exec
-- The seeder calls this once per boot before re-applying the YAML edges
-- so a deleted YAML edge is reflected in the DB. Safe because cascade
-- edges are derived content (catalogs/metrics/*.yaml is the source).
DELETE FROM metric_cascade_edges;

-- name: ListAllMetricCascadeEdges :many
-- Used by the seeder to compare existing edges + by the docs reference
-- generator.
SELECT parent_id, child_id, weight, notes
FROM metric_cascade_edges
ORDER BY parent_id, child_id;

-- name: InsertMetricObservation :one
-- Write one observation row. Runs in the caller's tenant context
-- (atlas_app pool + tenancy GUC). The source string distinguishes
-- 'evaluator:<name>' from 'manual:<user-uuid>' provenance.
INSERT INTO metric_observations (
    tenant_id, metric_id, observed_at, numeric_value, dimensions, source
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: ListMetricObservations :many
-- The series read for GET /v1/metrics/{id}/observations. Keyset
-- pagination via (observed_at, id) — the index over (tenant, metric,
-- observed_at DESC) lets the descending scan land contiguously.
-- Window bounds are optional; pass +/- infinity from the call site
-- when the caller omits them.
SELECT *
FROM metric_observations
WHERE metric_id = $1
  AND observed_at >= @since::timestamptz
  AND observed_at <= @until::timestamptz
ORDER BY observed_at DESC, id DESC
LIMIT $2;

-- name: GetMetricTarget :one
-- Returns the tenant's target row for one metric. ErrNoRows when unset.
SELECT *
FROM metric_targets
WHERE metric_id = $1;

-- name: UpsertMetricTarget :one
-- Idempotent upsert keyed on (tenant_id, metric_id). The tenant_id is
-- passed explicitly so the WITH CHECK clauses can verify against the
-- caller's GUC.
INSERT INTO metric_targets (
    tenant_id, metric_id, target_value, warning_threshold, critical_threshold,
    direction, owner_user_id, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (tenant_id, metric_id) DO UPDATE
SET target_value       = EXCLUDED.target_value,
    warning_threshold  = EXCLUDED.warning_threshold,
    critical_threshold = EXCLUDED.critical_threshold,
    direction          = EXCLUDED.direction,
    owner_user_id      = EXCLUDED.owner_user_id,
    notes              = EXCLUDED.notes,
    updated_at         = now()
RETURNING *;

-- name: InsertMetricInput :one
-- Append one manual entry. The fn_metric_inputs_replicate trigger fires
-- AFTER INSERT and writes a matching metric_observations row so the
-- read API serves both shapes from one series. Idempotency boundary is
-- the HTTP handler upstream (the trigger is unconditional).
INSERT INTO metric_inputs (
    tenant_id, metric_id, input_at, numeric_value, dimensions,
    entered_by_user_id, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListMetricInputs :many
-- The audit-trail read. Used by the per-metric inputs view (post-v1) and
-- by integration tests verifying the insert trigger semantics.
SELECT *
FROM metric_inputs
WHERE metric_id = $1
ORDER BY input_at DESC, id DESC
LIMIT $2;

-- name: ListTenantsForMetricsScheduler :many
-- The 15-min cron enumerates tenants from the migrator pool (BYPASSRLS)
-- so it can iterate every tenant; each evaluator then runs through the
-- app pool with tenancy.WithTenant applied. Same shape as
-- ListTenantsWithActiveControls (slice 016) but scoped to "tenants that
-- have any presence in the platform" — for now, the union of every
-- tenant_id appearing in metrics-relevant primitives.
--
-- Implementation: union the tenant_id columns across controls,
-- evidence_records, and risks — the cheapest broad signal that a
-- tenant exists. A tenant with zero rows in all three has nothing to
-- measure and is skipped.
SELECT DISTINCT t.tenant_id::uuid AS tenant_id
FROM (
    SELECT tenant_id FROM controls         WHERE tenant_id IS NOT NULL
    UNION
    SELECT tenant_id FROM evidence_records WHERE tenant_id IS NOT NULL
    UNION
    SELECT tenant_id FROM risks            WHERE tenant_id IS NOT NULL
) t
WHERE t.tenant_id IS NOT NULL;
