-- Slice 018: FrameworkScope predicate + four-state workflow queries.
--
-- Conventions match slice 014/017/019:
--   * `tenant_id` is always the first parameter (RLS reads it via the GUC,
--     but we additionally pass it explicitly so query plans use the index).
--   * Time-sensitive transitions (`now()`) are issued by the DB so the
--     application doesn't have to choose a clock.
--   * Workflow transition queries use guarded UPDATEs (`AND state = '<from>'`)
--     so concurrent transitions fail loudly with rowcount=0 rather than
--     silently overwriting state.

-- name: CreateFrameworkScope :one
-- Insert a new framework_scope row in `draft` state. The predicate is the
-- canonicalized JSON the application produced; predicate_hash is sha256 of
-- the same JSON (the application computes and passes both to keep the DB
-- trigger comparison cheap).
INSERT INTO framework_scopes (
    id, tenant_id, framework_version_id, name,
    state, predicate, predicate_hash
)
VALUES (
    $1, $2, $3, $4,
    'draft', $5, $6
)
RETURNING *;

-- name: GetFrameworkScopeByID :one
SELECT *
FROM framework_scopes
WHERE tenant_id = $1 AND id = $2;

-- name: ListFrameworkScopes :many
-- Newest first. Caller filters by framework_version + state in the
-- application layer because sqlc-static optional WHERE is noisy; the row
-- count per tenant is bounded by (#frameworks × scope-versions) which stays
-- under a few hundred for any realistic deployment.
SELECT *
FROM framework_scopes
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: ListFrameworkScopesByFrameworkVersion :many
SELECT *
FROM framework_scopes
WHERE tenant_id = $1 AND framework_version_id = $2
ORDER BY created_at DESC, id ASC;

-- name: GetActivatedFrameworkScope :one
-- Returns the currently-active framework scope for a given framework version,
-- i.e. the (at most one) row in state `activated` for that (tenant, fv) pair.
-- AC-3: a partial UNIQUE index guarantees at most one row matches.
SELECT *
FROM framework_scopes
WHERE tenant_id = $1
  AND framework_version_id = $2
  AND state = 'activated';

-- name: GetFrameworkScopeAsOf :one
-- AC-13: historical query — return the row that was the active scope at the
-- supplied timestamp. The row whose effective_from <= as_of AND (the row was
-- not yet superseded at as_of OR was never superseded).
-- ORDER BY effective_from DESC LIMIT 1 picks the most-recent applicable row.
SELECT *
FROM framework_scopes
WHERE tenant_id = $1
  AND framework_version_id = $2
  AND effective_from IS NOT NULL
  AND effective_from <= $3
  AND (superseded_at IS NULL OR superseded_at > $3)
ORDER BY effective_from DESC, id ASC
LIMIT 1;

-- name: UpdateFrameworkScopePredicate :one
-- Patch the predicate. The BEFORE UPDATE trigger
-- (framework_scopes_bounce_on_predicate_change_trg) ensures that if this row
-- was in `review` or `approved`, NEW.state falls back to `draft` and the
-- approval columns are nulled. Application code reads back the row state to
-- surface the `approval_invalidated` banner per AC-9.
UPDATE framework_scopes
SET predicate = $3,
    predicate_hash = $4,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: SubmitFrameworkScope :one
-- AC-6: draft -> review. Guarded so callers can never re-submit an already
-- under-review row by accident (would still be valid but masks bugs).
UPDATE framework_scopes
SET state = 'review',
    updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND state = 'draft'
RETURNING *;

-- name: ApproveFrameworkScope :one
-- AC-7: review -> approved. Records approver_user_id, approved_at, and the
-- predicate_hash captured at this moment so a later predicate edit can prove
-- "the approval was for THIS text, not the current text" (ADR-0001 §positive).
-- Optional approval-evidence file URL + hash are passed by the handler.
UPDATE framework_scopes
SET state = 'approved',
    approver_user_id = $3,
    approved_at = now(),
    predicate_hash_at_approval = predicate_hash,
    approval_evidence_file_url = $4,
    approval_evidence_file_hash = $5,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND state = 'review'
RETURNING *;

-- name: ActivateFrameworkScope :one
-- AC-8: approved -> activated. The caller must supply effective_from
-- (timestamptz). The application supersedes the prior `activated` row in
-- the same transaction by calling SupersedePreviousActivated below; both
-- statements run inside one tx so the partial unique index never sees
-- two `activated` rows at once.
UPDATE framework_scopes
SET state = 'activated',
    effective_from = $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND state = 'approved'
RETURNING *;

-- name: SupersedePreviousActivated :exec
-- AC-8: in the same transaction as ActivateFrameworkScope, transition the
-- previously-activated row (if any) to `superseded`, point its
-- superseded_by at the new row, stamp superseded_at = now(). Skips the
-- new row itself (id <> @new_id) so a re-running activation is idempotent.
UPDATE framework_scopes
SET state = 'superseded',
    superseded_by = $3,
    superseded_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND framework_version_id = $2
  AND id <> $3
  AND state = 'activated';

-- name: DeleteFrameworkScope :exec
-- Used by tests + cleanup paths. Production deployments will rarely delete
-- a scope (supersession is the lifecycle exit); the row is preserved as
-- audit trail.
DELETE FROM framework_scopes
WHERE tenant_id = $1 AND id = $2;
