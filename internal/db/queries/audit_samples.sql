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
-- the explicit "no horizon" semantic. frozen_at bounds ingested_at as well
-- (#1699): a record written after the freeze with a backdated observed_at
-- never joins the frozen population. amended_ids adds the records a freeze
-- revision put in the period's population (#1742); empty for none.
--
-- Scope predicate filtering happens AFTER this count -- the application
-- evaluates the JSON-AST against scope cells in memory and intersects with
-- evidence_records.scope_id. v1 keeps the SQL simple; future slices can
-- push the predicate down into a JSONB index.
-- #2817: plus the tenant's unbound records of a kind in unbound_kinds that
-- the control's evidence_queries declare (internal/evidence/unbound).
SELECT COUNT(*)::bigint AS row_count
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND observed_at >= $3
  AND observed_at <= $4
  AND ((observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz)
        AND ingested_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz))
       OR (id = ANY(sqlc.arg(amended_ids)::uuid[])
           AND observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz)));

-- name: ListPopulationEvidenceIDs :many
-- The deterministic ordering by id is load-bearing for AC-2: the sampler
-- runs Fisher-Yates over THIS ordered slice, so the order must be stable
-- across calls. id is the primary key (UUID); the tie-breaker is unused
-- because ids are unique.
--
-- Returns just the evidence_record_id (and observed_at for scope intersection)
-- to keep the round-trip small; the handler hydrates after the shuffle.
-- #2817: plus the tenant's unbound records of a kind in unbound_kinds that
-- the control's evidence_queries declare (internal/evidence/unbound).
SELECT id, scope_id
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND observed_at >= $3
  AND observed_at <= $4
  AND ((observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz)
        AND ingested_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz))
       OR (id = ANY(sqlc.arg(amended_ids)::uuid[])
           AND observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz, 'infinity'::timestamptz)))
ORDER BY id;

-- name: CreateSample :one
-- #1742: freeze_revision is the revision of the frozen period the
-- population drew from (GetPopulationFreezeRevision), NULL otherwise.
INSERT INTO samples (
    id, tenant_id, population_id, n, seed, created_by, freeze_revision
)
VALUES ($1, $2, $3, $4, $5, $6, sqlc.narg('freeze_revision'))
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
    n_requested, n_returned, reason_code, subject_module,
    freeze_revision
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'core', sqlc.narg('freeze_revision'))
RETURNING *;

-- name: ListSampleAuditLog :many
SELECT *
FROM sample_audit_log
WHERE tenant_id = $1
ORDER BY occurred_at DESC, id ASC
LIMIT $2;

-- name: GetSamplePopulationPeriod :one
-- #2157: the audit period a sample belongs to, through its population. The
-- authz hydrator puts it on input.resource.attrs.audit_period_id so the
-- auditor rules in audit_periods.rego / auditor.rego can match it. NULL
-- when the population is attached to no period.
SELECT p.audit_period_id
FROM samples s
JOIN populations p
    ON p.tenant_id = s.tenant_id AND p.id = s.population_id
WHERE s.tenant_id = $1 AND s.id = $2;

-- name: ListControlIDsDeclaringEvidenceKind :many
-- #2817: the controls whose evidence_queries declare evidence_kind. An
-- unbound record of a kind in internal/evidence/unbound belongs to each of
-- them, so the authz hydrator looks for its periods on all of them.
SELECT id FROM controls
WHERE tenant_id = $1
  AND evidence_queries @> jsonb_build_array(
        jsonb_build_object('evidence_kind', sqlc.arg(evidence_kind)::text))
ORDER BY id;

-- name: ListPeriodPopulationsForControl :many
-- #2157: the populations on one control that are attached to an audit
-- period. The authz hydrator tests an evidence record (or a control and
-- scope cell) against each one to find the periods it belongs to.
SELECT * FROM populations
WHERE tenant_id = $1
  AND control_id = $2
  AND audit_period_id IS NOT NULL
ORDER BY id;

-- name: GetSampleAmendReason :one
-- #1742: the admin's reason for the freeze revision a sample was drawn
-- from, when that revision is an import amendment (revision >= 2). No row
-- otherwise.
SELECT r.justification
FROM samples s
JOIN populations p ON p.tenant_id = s.tenant_id AND p.id = s.population_id
JOIN audit_period_freeze_revisions r
  ON r.tenant_id = s.tenant_id
 AND r.audit_period_id = p.audit_period_id
 AND r.revision = s.freeze_revision
WHERE s.tenant_id = $1
  AND s.id        = $2;
