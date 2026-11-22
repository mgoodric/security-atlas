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
    triggered_by
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12,
    sqlc.narg(triggered_by)
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
-- This is a pure SELECT — the engine never mutates the ledger.
SELECT id, tenant_id, control_id, control_ref, scope_id,
       observed_at, result, freshness_class, hash, payload,
       evidence_kind, ingested_at
FROM evidence_records
WHERE evidence_records.tenant_id = sqlc.arg(tenant_id)
  AND (control_id = sqlc.arg(control_id)
       OR (control_ref = sqlc.arg(control_ref) AND evidence_kind <> ALL(sqlc.arg(routed_kinds)::text[]))
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = sqlc.arg(control_id)
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND observed_at <= sqlc.arg(observed_at)
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
