-- Slice 012 — control state evaluation engine queries.
--
-- `control_evaluations` is the append-only output table of the evaluation
-- stage (canvas §4.3). The engine reads `evidence_records` (the immutable
-- ledger) and `controls` (slice 009 bundles), computes derived state, and
-- appends rows here. There is NO UpdateControlEvaluation / NO
-- DeleteControlEvaluation query — the table is append-only at both the RLS
-- layer (no UPDATE/DELETE policy under FORCE) and this sqlc surface.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee (canvas invariant #6). Every timestamp cutoff is computed in Go
-- and passed as an explicit parameter — never a single-placeholder
-- expression that would trip pgx type inference (SQLSTATE 42P08).

-- name: InsertControlEvaluation :one
-- Append one evaluation row. The ONLY write this slice performs. Note the
-- absence of any INSERT into evidence_records — the engine has no
-- evidence-write surface (constitutional invariant #2).
INSERT INTO control_evaluations (
    id, tenant_id, control_id, scope_cell_id, eval_run_id,
    evaluated_at, result, freshness_status,
    evidence_count_in_window, last_observed_at, freshness_class, trigger,
    triggered_by, evaluated_as_of
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12,
    sqlc.narg(triggered_by), sqlc.narg(evaluated_as_of)
)
RETURNING *;

-- name: ListEvidenceForControlAsOf :many
-- The evaluation engine's read of the evidence ledger for one control,
-- bounded by the point-in-time horizon `as_of` (AC-1's `?as-of=` and AC-7's
-- replay both pass an explicit horizon; live evaluation passes 'infinity').
-- Ordered observed_at DESC so the engine sees the freshest record first,
-- then ingested_at DESC and id DESC so records sharing an observed_at come
-- back in one fixed order (#2111: the latest attestation wins, and equal
-- timestamps must not make that choice depend on the plan).
-- unbound_kinds (#2817) adds the tenant's records of those kinds that are
-- bound to no control, when the control's evidence_queries declare the kind:
-- a policy acknowledgement names a policy, never a control. A record of such
-- a kind that IS bound reaches only its own control. internal/evidence/unbound
-- owns the list; every ledger reader below applies the same rule.
-- The horizon bounds ingested_at as well as observed_at (#1699, ISC-3): a
-- replay of T sees only the rows that were in the ledger at T, so a record
-- backdated to before T but written after it is not counted.
-- This is a pure SELECT — the engine never mutates the ledger.
SELECT id, tenant_id, control_id, control_ref, scope_id,
       observed_at, result, freshness_class, hash, payload,
       evidence_kind, ingested_at
FROM evidence_records
WHERE evidence_records.tenant_id = sqlc.arg(tenant_id)
  AND (control_id = sqlc.arg(control_id)
       OR (control_ref = sqlc.arg(control_ref) AND (evidence_kind IS NULL OR evidence_kind <> ALL(sqlc.arg(routed_kinds)::text[])))
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = sqlc.arg(control_id)
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND observed_at <= sqlc.arg(observed_at)
  AND ingested_at <= sqlc.arg(observed_at)
ORDER BY observed_at DESC, ingested_at DESC, id DESC;

-- name: ListLatestControlEvaluations :many
-- Every (control, scope_cell)'s latest state for one control. DISTINCT ON
-- collapses the append-only history to the current row per cell. Used by
-- GET /v1/controls/:id/state when no scope filter is supplied.
SELECT DISTINCT ON (scope_cell_id) *
FROM control_evaluations
WHERE tenant_id = $1
  AND control_id = $2
  AND evaluated_at <= $3
ORDER BY scope_cell_id, evaluated_at DESC, created_at DESC;

-- name: ListControlEvaluationsForEffectiveness :many
-- AC-6: every evaluation for one control inside the rolling-30-day window.
-- The cutoff is computed in Go (now - 30d) and passed explicitly so pgx
-- does not have to infer the type of a bare placeholder. The handler /
-- effectiveness.go aggregates these into the rolling pass rate.
SELECT result, evaluated_at, scope_cell_id
FROM control_evaluations
WHERE tenant_id = $1
  AND control_id = $2
  AND evaluated_at >= $3
  AND evaluated_at <= $4
ORDER BY evaluated_at DESC;

-- name: ListTenantsWithActiveControls :many
-- The scheduled time-based recompute (AC-2) runs as the migrator role
-- (BYPASSRLS) so it can enumerate every tenant; the per-tenant evaluation
-- then applies the GUC inside its own transaction for RLS-honest writes.
-- This mirrors slice 021's ListTenantsWithActiveExceptions cross-tenant
-- sweep pattern.
SELECT DISTINCT tenant_id
FROM controls
WHERE superseded_by IS NULL;

-- name: ListInconclusiveStreaks :many
-- #2915: when each (control, scope_cell) that is inconclusive now became
-- inconclusive. The ledger is append-only and the scheduler writes a row
-- per run, so a cell's streak starts at its earliest evaluation after its
-- latest non-inconclusive one (or its first evaluation, if it has never
-- been anything else). A cell whose latest evaluation is not inconclusive
-- has no row after its last break, so it returns nothing. Callers pass the
-- controls they already know are inconclusive.
--
-- #2985: the cost is index probes, not history length. Per cell, the last
-- break is one probe on the partial index
-- idx_control_evaluations_not_inconclusive and the streak start is one
-- probe on idx_control_evaluations_latest, so a year of hourly scheduler
-- rows costs what a day's does. The cells come from the skip scan
-- ListActiveControlsWithState uses (#2765). The NULL whole-tenant cell is
-- its own branch, because = cannot match NULL.
WITH RECURSIVE ids AS (
    SELECT DISTINCT unnest(sqlc.arg(control_ids)::uuid[]) AS control_id
),
cells AS (
    SELECT ids.control_id, f.scope_cell_id
    FROM ids
    CROSS JOIN LATERAL (
        SELECT ce.scope_cell_id
        FROM control_evaluations ce
        WHERE ce.tenant_id = sqlc.arg(tenant_id) AND ce.control_id = ids.control_id
          AND ce.scope_cell_id IS NOT NULL
        ORDER BY ce.scope_cell_id
        LIMIT 1
    ) f
    UNION ALL
    SELECT c.control_id, n.scope_cell_id
    FROM cells c
    CROSS JOIN LATERAL (
        SELECT ce.scope_cell_id
        FROM control_evaluations ce
        WHERE ce.tenant_id = sqlc.arg(tenant_id) AND ce.control_id = c.control_id
          AND ce.scope_cell_id > c.scope_cell_id
        ORDER BY ce.scope_cell_id
        LIMIT 1
    ) n
)
SELECT c.control_id::uuid AS control_id, c.scope_cell_id, s.evaluated_at::timestamptz AS inconclusive_since
FROM cells c
LEFT JOIN LATERAL (
    SELECT ce.evaluated_at
    FROM control_evaluations ce
    WHERE ce.tenant_id = sqlc.arg(tenant_id) AND ce.control_id = c.control_id
      AND ce.scope_cell_id = c.scope_cell_id
      AND ce.result <> 'inconclusive'
      AND ce.evaluated_at <= sqlc.arg(as_of)
    ORDER BY ce.evaluated_at DESC
    LIMIT 1
) b ON true
CROSS JOIN LATERAL (
    SELECT ce.evaluated_at
    FROM control_evaluations ce
    WHERE ce.tenant_id = sqlc.arg(tenant_id) AND ce.control_id = c.control_id
      AND ce.scope_cell_id = c.scope_cell_id
      AND ce.evaluated_at > COALESCE(b.evaluated_at, '-infinity'::timestamptz)
      AND ce.evaluated_at <= sqlc.arg(as_of)
    ORDER BY ce.evaluated_at
    LIMIT 1
) s
UNION ALL
SELECT ids.control_id, NULL::uuid, s.evaluated_at::timestamptz
FROM ids
LEFT JOIN LATERAL (
    SELECT ce.evaluated_at
    FROM control_evaluations ce
    WHERE ce.tenant_id = sqlc.arg(tenant_id) AND ce.control_id = ids.control_id
      AND ce.scope_cell_id IS NULL
      AND ce.result <> 'inconclusive'
      AND ce.evaluated_at <= sqlc.arg(as_of)
    -- Leading scope_cell_id (constant here) lets the planner read the
    -- index in order: IS NULL does not make it a pathkey constant.
    ORDER BY ce.scope_cell_id, ce.evaluated_at DESC
    LIMIT 1
) b ON true
CROSS JOIN LATERAL (
    SELECT ce.evaluated_at
    FROM control_evaluations ce
    WHERE ce.tenant_id = sqlc.arg(tenant_id) AND ce.control_id = ids.control_id
      AND ce.scope_cell_id IS NULL
      AND ce.evaluated_at > COALESCE(b.evaluated_at, '-infinity'::timestamptz)
      AND ce.evaluated_at <= sqlc.arg(as_of)
    -- DESC so a backward scan of the index (evaluated_at DESC) serves
    -- the ascending evaluated_at with no sort.
    ORDER BY ce.scope_cell_id DESC, ce.evaluated_at
    LIMIT 1
) s;
