-- evidence_records + evidence_audit_log queries for slice 013.
--
-- The append-only ledger contract: only INSERT and SELECT — no UPDATE,
-- no DELETE. The schema enforces this at the RLS layer (no UPDATE /
-- DELETE policy on the table); these queries enforce it at the sqlc
-- surface (no UpdateEvidenceRecord, no DeleteEvidenceRecord exists).

-- name: InsertEvidenceRecord :one
-- Slice 474: scope_canonical persists the canonical (sorted) wire scope the
-- content-hash was computed over, so `atlas evidence verify` can reconstruct
-- the exact record and recompute an identical hash.
-- Slice 633: observed_at_nanos persists the LOSSLESS Unix-nanosecond value of
-- the wire observed_at (the observed_at TIMESTAMPTZ column is microsecond
-- precision and truncates sub-us nanos), so the verify walk reconstructs the
-- exact nanosecond timestamp the hash covered.
INSERT INTO evidence_records (
    id, tenant_id,
    control_id, control_ref, scope_id,
    observed_at, provenance, result,
    payload, payload_uri,
    hash, freshness_class, valid_until,
    idempotency_key, evidence_kind, schema_version,
    credential_id, ingestion_path, source_attribution,
    scope_canonical, observed_at_nanos
) VALUES (
    $1, $2,
    $3, $4, $5,
    $6, $7, $8,
    $9, $10,
    $11, $12, $13,
    $14, $15, $16,
    $17, $18, $19,
    $20, $21
)
RETURNING *;

-- name: GetEvidenceRecordByIdempotency :one
-- Hot path for the push handler: "is there already a record under
-- (tenant_id, idempotency_key)?". Returns at most one row because the
-- partial UNIQUE index `evidence_records_tenant_idem_uniq` enforces it.
SELECT *
FROM evidence_records
WHERE tenant_id = $1
  AND idempotency_key = $2
LIMIT 1;

-- name: GetEvidenceRecordByID :one
SELECT *
FROM evidence_records
WHERE id = $1
  AND tenant_id = $2;

-- name: ListEvidenceRecordsByControl :many
-- Replay-friendly read for evaluation (slice 012+). Append-only ordering
-- on observed_at lets the evaluator stream historical state without
-- worrying about UPDATEs since slice 002.
SELECT *
FROM evidence_records
WHERE tenant_id = $1
  AND (control_id = $2 OR control_ref = $3)
ORDER BY observed_at DESC
LIMIT $4 OFFSET $5;

-- name: CountEvidenceRecordsByTenant :one
SELECT count(*) FROM evidence_records WHERE tenant_id = $1;

-- name: WalkEvidenceRecordsForVerify :many
-- Slice 464: keyset-paginated ledger walk for `atlas evidence verify`.
-- Read-only integrity walk — recomputes each record's canonical hash and
-- compares to the stored `hash`. Ordered by id ASC so the caller can page
-- with a cursor (last-seen id) without OFFSET drift on a large ledger.
-- Tenant-scoped: RLS bounds the rows to the current tenant; the @after_id
-- cursor and @page_size keep the working set bounded regardless of ledger
-- size. The empty-UUID sentinel ('00000000-...') seeds the first page.
SELECT *
FROM evidence_records
WHERE tenant_id = $1
  AND id > sqlc.arg('after_id')
ORDER BY id ASC
LIMIT sqlc.arg('page_size');

-- name: InsertEvidenceAuditEntry :one
-- AC-7: every push attempt — accepted or rejected — lands in the audit
-- log keyed by credential id. The platform layer writes one row per
-- decision; rejections include reason_code (validation, idempotency
-- mismatch, scope violation, etc.).
--
-- Slice 180: explicit `subject_module='core'` (column defaults to 'core' at
-- the DB layer; explicit-is-clearer per AC-5).
INSERT INTO evidence_audit_log (
    id, tenant_id, credential_id,
    decision, reason_code,
    idempotency_key, evidence_kind, record_id, subject_module
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, 'core'
)
RETURNING *;

-- name: ListEvidenceAuditEntriesByCredential :many
SELECT *
FROM evidence_audit_log
WHERE tenant_id = $1 AND credential_id = $2
ORDER BY received_at DESC
LIMIT $3 OFFSET $4;
