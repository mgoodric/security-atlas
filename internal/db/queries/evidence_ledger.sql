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
-- #2817: plus the tenant's unbound records of a kind in unbound_kinds that
-- the control's evidence_queries declare (internal/evidence/unbound).
-- The same leg is on the three by-control readers below.
-- Replay-friendly read for evaluation (slice 012+). Append-only ordering
-- on observed_at lets the evaluator stream historical state without
-- worrying about UPDATEs since slice 002.
SELECT *
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2 OR (control_ref = $3 AND (evidence_kind IS NULL OR evidence_kind <> ALL(sqlc.arg(routed_kinds)::text[])))
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
ORDER BY observed_at DESC
LIMIT $4 OFFSET $5;

-- name: CountEvidenceRecordsByTenant :one
SELECT count(*) FROM evidence_records WHERE tenant_id = $1;

-- name: CountEvidenceRecordsByControl :one
-- Slice 502: total CURRENT LIVE evidence count for one control, used by the
-- evidence-summary surface to render a "showing N of M" bound (the summary is
-- over the bounded top-N, never the full history — P0-502-8). Resolution
-- mirrors ListEvidenceRecordsByControl: (control_id = $2 OR control_ref = $3).
SELECT count(*)
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2 OR (control_ref = $3 AND (evidence_kind IS NULL OR evidence_kind <> ALL(sqlc.arg(routed_kinds)::text[])))
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))));

-- name: ListEvidenceRecordsByControlBeforeHorizon :many
-- Slice 749: the FROZEN-population variant of ListEvidenceRecordsByControl.
-- Returns the top-N most-recent evidence records for one control bounded by an
-- audit-period freeze horizon: observed_at <= frozen_at (invariant #10). The
-- bound is the period's audit_periods.frozen_at, passed by the caller after it
-- has resolved the period; this query NEVER reads the period row itself, it only
-- applies the horizon it is given. Mirrors ListEvidenceRecordsByControl's
-- (control_id = $2 OR control_ref = $3) resolution and observed_at DESC order so
-- the bounded top-N is the N most-recent records WITHIN the frozen population —
-- never a post-freeze (live) record (P0-749-1). The COALESCE-to-infinity on a
-- NULL horizon mirrors the slice-026/028 frozen-sample read path: an open period
-- (frozen_at NULL) falls through to live state, but the slice-749 surface only
-- summarizes FROZEN periods, so in practice the horizon is always set. The
-- horizon bounds ingested_at too (#1699): a record written after the freeze
-- with a backdated observed_at is not in the frozen population. amended_ids
-- adds the records a later freeze revision put in the population (#1742);
-- pass an empty array for the original freeze.
SELECT *
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2 OR (control_ref = $3 AND (evidence_kind IS NULL OR evidence_kind <> ALL(sqlc.arg(routed_kinds)::text[])))
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND ((observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz,
                               'infinity'::timestamptz)
        AND ingested_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz,
                                    'infinity'::timestamptz))
       OR id = ANY(sqlc.arg(amended_ids)::uuid[]))
ORDER BY observed_at DESC
LIMIT $4 OFFSET $5;

-- name: CountEvidenceRecordsByControlBeforeHorizon :one
-- Slice 749: total FROZEN-population evidence count for one control bounded by
-- the audit-period freeze horizon (observed_at <= frozen_at), for the
-- "showing N of M" UI label. Mirrors CountEvidenceRecordsByControl's resolution
-- and the ListEvidenceRecordsByControlBeforeHorizon horizon predicate so the
-- count and the bounded list agree on the frozen population (P0-749-1).
SELECT count(*)
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2 OR (control_ref = $3 AND (evidence_kind IS NULL OR evidence_kind <> ALL(sqlc.arg(routed_kinds)::text[])))
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND ((observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz,
                               'infinity'::timestamptz)
        AND ingested_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz,
                                    'infinity'::timestamptz))
       OR id = ANY(sqlc.arg(amended_ids)::uuid[]));

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
--
-- #1742: import_batch_id ties a historical-import attempt to its batch;
-- NULL on every other ingestion path.
INSERT INTO evidence_audit_log (
    id, tenant_id, credential_id,
    decision, reason_code,
    idempotency_key, evidence_kind, record_id, subject_module,
    import_batch_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, 'core',
    sqlc.narg('import_batch_id')
)
RETURNING *;

-- name: ListEvidenceAuditEntriesByCredential :many
SELECT *
FROM evidence_audit_log
WHERE tenant_id = $1 AND credential_id = $2
ORDER BY received_at DESC
LIMIT $3 OFFSET $4;

-- name: GetEvidenceAuditEntryByReceipt :one
-- Pusher-visible receipt lookup. Scoped to tenant + credential so a
-- credential can only see decisions for its own pushed records.
SELECT *
FROM evidence_audit_log
WHERE tenant_id = $1
  AND credential_id = $2
  AND record_id = $3
ORDER BY received_at DESC
LIMIT 1;

-- name: ExistsAttestationAfter :one
-- #2520: is there an attestation for the control later than the given one,
-- under the evaluation engine's latest-attestation order (internal/eval
-- latestAttestationWins): min(observed_at, ingested_at), then ingested_at,
-- then id compared bytewise? The attest handler asks this on a dedupe hit
-- for a server-derived key, so a repeat of an older attestation is recorded
-- when a newer one would otherwise stand. Control resolution mirrors
-- ListEvidenceForControlAsOf.
SELECT EXISTS (
    SELECT 1
    FROM evidence_records
    WHERE tenant_id = sqlc.arg(tenant_id)
      AND (control_id = sqlc.arg(control_id) OR control_ref = sqlc.arg(control_ref)::text)
      AND evidence_kind = sqlc.arg(evidence_kind)::text
      AND id <> sqlc.arg(id)
      AND (
        LEAST(observed_at, ingested_at) > sqlc.arg(attested_at)::timestamptz
        OR (LEAST(observed_at, ingested_at) = sqlc.arg(attested_at)::timestamptz
            AND ingested_at > sqlc.arg(ingested_at)::timestamptz)
        OR (LEAST(observed_at, ingested_at) = sqlc.arg(attested_at)::timestamptz
            AND ingested_at = sqlc.arg(ingested_at)::timestamptz
            AND id::text COLLATE "C" > sqlc.arg(id_text)::text)
      )
) AS newer;
