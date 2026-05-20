-- Slice 026 — sample-pull primitives.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee. None of these queries mutate evidence_records — the ledger
-- stays read-only on this path (anti-criterion P0).

-- name: CreatePopulation :one
-- Insert the population row. row_count is set by the application AFTER it
-- has resolved the population via CountPopulationEvidence below; doing it
-- in two statements keeps the SQL static (sqlc-friendly) and lets the
-- handler validate the count before persisting.
INSERT INTO populations (
    id, tenant_id, control_id, scope_predicate,
    time_window_start, time_window_end, frozen_at,
    row_count, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetPopulationByID :one
SELECT * FROM populations
WHERE tenant_id = $1 AND id = $2;

-- name: ListPopulationsByTenant :many
SELECT * FROM populations
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: CountPopulationEvidence :one
-- AC-1 + AC-5: count evidence records that match the population's filter.
-- AC-5 forward-compat: `observed_at <= COALESCE(frozen_at, 'infinity')`
-- is a no-op until slice 028 sets frozen_at. The COALESCE-to-infinity is
-- the explicit "no horizon" semantic.
--
-- Scope predicate filtering happens AFTER this count -- the application
-- evaluates the JSON-AST against scope cells in memory and intersects with
-- evidence_records.scope_id. v1 keeps the SQL simple; future slices can
-- push the predicate down into a JSONB index.
SELECT COUNT(*)::bigint AS row_count
FROM evidence_records
WHERE tenant_id = $1
  AND control_id = $2
  AND observed_at >= $3
  AND observed_at <= $4
  AND observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz);

-- name: ListPopulationEvidenceIDs :many
-- The deterministic ordering by id is load-bearing for AC-2: the sampler
-- runs Fisher-Yates over THIS ordered slice, so the order must be stable
-- across calls. id is the primary key (UUID); the tie-breaker is unused
-- because ids are unique.
--
-- Returns just the evidence_record_id (and observed_at for scope intersection)
-- to keep the round-trip small; the handler hydrates after the shuffle.
SELECT id, scope_id
FROM evidence_records
WHERE tenant_id = $1
  AND control_id = $2
  AND observed_at >= $3
  AND observed_at <= $4
  AND observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz)
ORDER BY id;

-- name: CreateSample :one
INSERT INTO samples (
    id, tenant_id, population_id, n, seed, created_by
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSampleByID :one
SELECT * FROM samples
WHERE tenant_id = $1 AND id = $2;

-- name: InsertSampleEvidence :exec
INSERT INTO sample_evidence (
    sample_id, tenant_id, evidence_record_id, ordinal
)
VALUES ($1, $2, $3, $4);

-- name: ListSampleEvidence :many
SELECT evidence_record_id, ordinal
FROM sample_evidence
WHERE tenant_id = $1 AND sample_id = $2
ORDER BY ordinal ASC;

-- name: UpsertSampleAnnotation :one
-- One annotation per (sample, evidence_record). Re-annotating overwrites
-- result + notes (the audit log still captures every attempt via
-- WriteSampleAuditLog).
INSERT INTO sample_annotations (
    id, tenant_id, sample_id, evidence_record_id,
    result, annotated_by, notes
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, sample_id, evidence_record_id) DO UPDATE
    SET result = EXCLUDED.result,
        annotated_by = EXCLUDED.annotated_by,
        annotated_at = now(),
        notes = EXCLUDED.notes
RETURNING *;

-- name: ListSampleAnnotations :many
SELECT *
FROM sample_annotations
WHERE tenant_id = $1 AND sample_id = $2
ORDER BY annotated_at DESC, id ASC;

-- name: WriteSampleAuditLog :one
-- Every sample pull writes one row here. The seed -> sample_id mapping
-- captured in (seed, sample_id) is the re-audit trail (AC-6).
--
-- Slice 180: explicit `subject_module='core'` (column defaults to 'core' at
-- the DB layer; explicit-is-clearer per AC-5).
INSERT INTO sample_audit_log (
    id, tenant_id, action, actor,
    population_id, sample_id, seed,
    n_requested, n_returned, reason_code, subject_module
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'core')
RETURNING *;

-- name: ListSampleAuditLog :many
SELECT *
FROM sample_audit_log
WHERE tenant_id = $1
ORDER BY occurred_at DESC, id ASC
LIMIT $2;
