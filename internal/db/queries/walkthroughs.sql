-- Slice 027 — walkthrough recording primitive.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer. The hash is computed in Go (sha256 over canonical
-- JSON, ADR 0003 content-only-inputs pattern) and persisted as canonical_hash
-- on the walkthroughs row. Tamper detection (AC-6) re-computes the hash on
-- the GET path and compares to the stored value.

-- name: CreateWalkthrough :one
-- Insert a walkthrough in status='draft' with the initial canonical_hash.
-- The hash is computed in Go over {control_id, narrative, transcript,
-- created_by, created_at, attachment_hashes[]} -- on create, the attachment
-- list is empty so the hash is over the no-attachment shape. The handler
-- recomputes the hash on every attachment addition and on finalize.
INSERT INTO walkthroughs (
    id, tenant_id, audit_period_id, control_id,
    narrative, transcript, canonical_hash, status,
    created_by, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, 'draft',
    $8, $9, now()
)
RETURNING *;

-- name: GetWalkthroughByID :one
SELECT * FROM walkthroughs
WHERE tenant_id = $1 AND id = $2;

-- name: ListWalkthroughsByTenant :many
SELECT * FROM walkthroughs
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: ListWalkthroughsByControl :many
SELECT * FROM walkthroughs
WHERE tenant_id = $1 AND control_id = $2
ORDER BY created_at DESC, id ASC;

-- name: UpdateWalkthroughHash :one
-- Re-stamps the canonical_hash + updated_at after an attachment is added.
-- Only succeeds when the walkthrough is in status='draft' -- the WHERE
-- guard makes a post-finalize mutation a zero-row UPDATE which the
-- handler surfaces as 409 Conflict.
UPDATE walkthroughs
SET canonical_hash = $3,
    updated_at     = now()
WHERE tenant_id = $1
  AND id        = $2
  AND status    = 'draft'
RETURNING *;

-- name: FinalizeWalkthrough :one
-- Flip status draft->finalized + stamp the as-finalized canonical_hash.
-- The hash on this row is the commitment auditors verify against; once
-- the row is finalized, the slice's tamper-detection re-compute compares
-- the live re-hash to this stored value.
UPDATE walkthroughs
SET status         = 'finalized',
    canonical_hash = $3,
    updated_at     = now()
WHERE tenant_id = $1
  AND id        = $2
  AND status    = 'draft'
RETURNING *;

-- name: CreateWalkthroughAttachment :one
-- Insert an attachment metadata row. The blob itself lives in the slice
-- 036 artifact store under storage_key. sha256_hash is the lowercase-hex
-- sha256 of the bytes, computed by the handler at upload time (the
-- slice 036 store does the same re-compute; both writes use the same
-- hash so verification is symmetric).
INSERT INTO walkthrough_attachments (
    id, tenant_id, walkthrough_id, storage_key,
    content_type, size_bytes, sha256_hash, annotations,
    uploaded_by, uploaded_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
RETURNING *;

-- name: ListWalkthroughAttachments :many
-- Ordered by uploaded_at then id so a re-list at any point in time
-- yields a deterministic sequence. The hash inputs are the SORTED
-- sha256_hash values, not the iteration order, so the list order does
-- not affect the walkthrough hash directly.
SELECT * FROM walkthrough_attachments
WHERE tenant_id = $1 AND walkthrough_id = $2
ORDER BY uploaded_at ASC, id ASC;

-- name: ListWalkthroughAttachmentHashes :many
-- AC-3 + AC-6: returns the sorted sha256_hash strings for the hash-input
-- computation. Sorted at the DB so a verifier's re-compute matches the
-- writer's commitment without trusting a Go-side sort.
SELECT sha256_hash
FROM walkthrough_attachments
WHERE tenant_id = $1 AND walkthrough_id = $2
ORDER BY sha256_hash;

-- name: WriteWalkthroughAuditLog :one
-- Append-only lifecycle log. action is DB-constrained
-- (walkthrough_created | attachment_added | walkthrough_finalized |
-- tamper_detected | mutation_rejected_frozen). detail captures action-
-- specific payload.
--
-- Slice 180: explicit `subject_module='core'` tags every write from the
-- core (non-privacy) module path. The column defaults to 'core' at the
-- DB level, so omitting this would still produce a 'core' row; explicit
-- is clearer and defense-in-depth (AC-5).
INSERT INTO walkthrough_audit_log (
    id, tenant_id, walkthrough_id, action, actor, detail, occurred_at, subject_module
)
VALUES ($1, $2, $3, $4, $5, $6, now(), 'core')
RETURNING *;

-- name: ListWalkthroughAuditLog :many
SELECT *
FROM walkthrough_audit_log
WHERE tenant_id = $1 AND walkthrough_id = $2
ORDER BY occurred_at DESC, id ASC;
