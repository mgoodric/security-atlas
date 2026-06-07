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
    evidence_count_in_window, last_observed_at, freshness_class, trigger
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12
)
RETURNING *;

-- name: ListEvidenceForControlAsOf :many
-- The evaluation engine's read of the evidence ledger for one control,
-- bounded by the point-in-time horizon `as_of` (AC-1's `?as-of=` and AC-7's
-- replay both pass an explicit horizon; live evaluation passes 'infinity').
-- Ordered observed_at DESC so the engine sees the freshest record first.
-- This is a pure SELECT — the engine never mutates the ledger.
SELECT id, tenant_id, control_id, control_ref, scope_id,
       observed_at, result, freshness_class, hash, payload
FROM evidence_records
WHERE tenant_id = $1
  AND (control_id = $2 OR control_ref = $3)
  AND observed_at <= $4
ORDER BY observed_at DESC;

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
