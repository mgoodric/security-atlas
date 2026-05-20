-- evidence_records + evidence_audit_log queries for slice 013.
--
-- The append-only ledger contract: only INSERT and SELECT — no UPDATE,
-- no DELETE. The schema enforces this at the RLS layer (no UPDATE /
-- DELETE policy on the table); these queries enforce it at the sqlc
-- surface (no UpdateEvidenceRecord, no DeleteEvidenceRecord exists).

-- name: InsertEvidenceRecord :one
INSERT INTO evidence_records (
    id, tenant_id,
    control_id, control_ref, scope_id,
    observed_at, provenance, result,
    payload, payload_uri,
    hash, freshness_class, valid_until,
    idempotency_key, evidence_kind, schema_version,
    credential_id, ingestion_path, source_attribution
) VALUES (
    $1, $2,
    $3, $4, $5,
    $6, $7, $8,
    $9, $10,
    $11, $12, $13,
    $14, $15, $16,
    $17, $18, $19
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
