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

-- ===== framework_scope_workflow_log (issue #1786) =====
--
-- Append-only history of framework_scopes changes. Written by
-- internal/frameworkscope.Store in the same tx as the change it records.
-- No UPDATE or DELETE query exists, and atlas_app holds no grant for one.

-- name: InsertFrameworkScopeWorkflowLog :exec
INSERT INTO framework_scope_workflow_log (
    tenant_id, framework_scope_id, action, from_state, to_state, before, after, actor
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
);

-- name: ListFrameworkScopeWorkflowLog :many
SELECT * FROM framework_scope_workflow_log
WHERE tenant_id = $1 AND framework_scope_id = $2
ORDER BY occurred_at, id;

-- ===== framework_scope_applicability (issue #1787) =====
--
-- Statement of Applicability decisions on a framework-scope revision. The
-- guard + reset triggers on the table enforce the workflow; these queries
-- only read and upsert.

-- name: GetFrameworkRequirementInVersion :one
SELECT id, code, title
FROM framework_requirements
WHERE id = $1 AND framework_version_id = $2;

-- name: GetFrameworkScopeApplicability :one
SELECT *
FROM framework_scope_applicability
WHERE tenant_id = $1 AND framework_scope_id = $2 AND framework_requirement_id = $3;

-- name: UpsertFrameworkScopeApplicability :one
INSERT INTO framework_scope_applicability (
    tenant_id, framework_scope_id, framework_requirement_id,
    decision, justification, implementation_ref, updated_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (framework_scope_id, framework_requirement_id) DO UPDATE
SET decision = EXCLUDED.decision,
    justification = EXCLUDED.justification,
    implementation_ref = EXCLUDED.implementation_ref,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: ListFrameworkScopeApplicability :many
-- Every requirement of the framework version, with the revision's decision
-- when one exists. A NULL framework_scope_id (no revision in force) returns
-- every requirement undecided.
SELECT r.id AS requirement_id,
       r.code,
       r.title,
       a.decision,
       a.justification,
       a.implementation_ref,
       a.updated_by,
       a.updated_at
FROM framework_requirements r
LEFT JOIN framework_scope_applicability a
       ON a.framework_requirement_id = r.id
      AND a.tenant_id = @tenant_id
      AND a.framework_scope_id = sqlc.narg(framework_scope_id)
WHERE r.framework_version_id = @framework_version_id
ORDER BY r.code, r.id;

-- name: GetActivatedExclusionForRequirement :one
-- The exclusion, if any, the tenant's ACTIVATED scope revision records for a
-- requirement (#1788). Per-requirement coverage reads report such a
-- requirement as `excluded` instead of uncovered. No row means the
-- requirement is applicable or undecided, or no revision is activated.
SELECT fsa.framework_scope_id,
       fsa.justification,
       fsa.implementation_ref,
       fsa.updated_at
FROM framework_scope_applicability fsa
JOIN framework_scopes fs
  ON fs.id = fsa.framework_scope_id
 AND fs.tenant_id = fsa.tenant_id
WHERE fsa.tenant_id = @tenant_id
  AND fsa.framework_requirement_id = @framework_requirement_id
  AND fsa.decision = 'excluded'
  AND fs.state = 'activated';
