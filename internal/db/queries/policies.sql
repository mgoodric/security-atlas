-- Slice 022 — policy library queries.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee. The state machine
--   draft -> under_review -> approved -> published -> superseded
-- is enforced by per-transition UPDATEs that include `WHERE status = '...'`
-- as the prior-state guard; zero affected rows means the transition was
-- attempted from the wrong state (the application probes after to
-- disambiguate ErrNotFound vs ErrWrongState, matching slice 021's pattern).
-- Publishing a later version is a two-step operation (supersede prior +
-- publish the successor row in place); the application wraps both queries
-- in a single tx so the chain is atomic.

-- name: CreatePolicy :one
INSERT INTO policies (
    id, tenant_id, predecessor_id,
    title, version, body_md,
    owner_role, approver_role,
    linked_control_ids, acknowledgment_required_roles,
    status, source_attribution, created_by
)
VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, $8,
    $9, $10,
    'draft', $11, $12
)
RETURNING *;

-- name: GetPolicyByID :one
SELECT *
FROM policies
WHERE tenant_id = $1 AND id = $2;

-- name: ListPolicies :many
-- Returns every policy for the tenant, newest first. Handler applies
-- status filter in-memory (cardinality is small per canvas v1 scope).
SELECT *
FROM policies
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: ListPoliciesWithAckRate :many
-- Slice 107 — paginated policy list LEFT JOINed to the per-policy
-- acknowledgment-rate cell. Single query; the handler MUST NOT loop
-- per-policy (anti-criterion P0 -- the whole point of the extension).
--
-- Shape:
--   1. The base policies SELECT is identical to ListPolicies (same
--      columns, same WHERE, same ORDER BY) so the result row's
--      first N columns scan into the existing dbx.Policy struct
--      verbatim.
--   2. Two scalar subqueries compute the denominator + numerator
--      ONLY for rows with status = 'published'. A CASE WHEN wraps
--      the subqueries so non-published rows return NULL for both
--      columns (the handler renders `ack_rate: null`).
--   3. The denominator subquery mirrors CountRequiredRoleUsersForVersion
--      verbatim (distinct issued_by in api_keys whose owner_roles
--      intersect the policy's acknowledgment_required_roles, OR is_admin).
--      Revoked keys excluded.
--   4. The numerator subquery mirrors CountFreshAcksForVersion verbatim
--      (distinct user_ids in policy_acknowledgments for THIS policy
--      version, acknowledged_at >= freshness_cutoff, gated on the same
--      role-intersect EXISTS predicate). The freshness cutoff is a
--      parameter so tests inject without time-bombing.
--
-- Constitutional invariants:
--   #6 RLS: policies + api_keys + policy_acknowledgments are tenant-scoped
--      under FORCE ROW LEVEL SECURITY. The tenant GUC the slice-033
--      middleware sets filters every table this query touches.
--   #9 Manual evidence first-class: this query is uniform whether the
--      policy was manually authored or imported from a vendor template.
--
-- See policy_acknowledgments.sql `CountRequiredRoleUsersForVersion` and
-- `CountFreshAcksForVersion` for the canonical predicates this query
-- mirrors -- they MUST stay in sync. The HTTP handler GET
-- /v1/policies/{id}/acknowledgment-rate calls those queries directly
-- per-policy; this slice runs the same math in one round-trip for the
-- list view.
-- Slice 159 Option C: the per-policy ack-rate cells are computed in
-- a CTE that filters `WHERE p.status = 'published'` and is then
-- LEFT JOINed back to the full policy list. sqlc v1.31.1 sees the
-- LEFT JOIN as nullable and emits `*int64` (under
-- `emit_pointers_for_null_types: true`) — non-published policies
-- get nil pointers for both ack columns. The slice-107 handler is
-- updated to use the pointer-style API (`r.AckDenominator != nil`
-- + `*r.AckDenominator`). JSON response shape is unchanged: nil
-- pointers marshal to `null`, populated pointers marshal to the
-- bigint value. See
-- `docs/audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md`.
WITH ack_cells AS (
    SELECT
        p.id AS policy_id,
        (SELECT COUNT(DISTINCT k.issued_by)::bigint
         FROM api_keys k
         WHERE k.tenant_id = p.tenant_id
           AND k.revoked_at IS NULL
           AND k.issued_by IS NOT NULL
           AND (
               k.is_admin = true
               OR k.owner_roles && p.acknowledgment_required_roles
           )
        ) AS ack_denominator,
        (SELECT COUNT(DISTINCT pa.user_id)::bigint
         FROM policy_acknowledgments pa
         WHERE pa.tenant_id = p.tenant_id
           AND pa.policy_version_id = p.id
           AND pa.acknowledged_at >= sqlc.arg('freshness_cutoff')::timestamptz
           AND EXISTS (
               SELECT 1
               FROM api_keys k
               WHERE k.tenant_id = pa.tenant_id
                 AND k.issued_by = pa.user_id
                 AND k.revoked_at IS NULL
                 AND (
                     k.is_admin = true
                     OR k.owner_roles && p.acknowledgment_required_roles
                 )
           )
        ) AS ack_numerator
    FROM policies p
    WHERE p.tenant_id = $1
      AND p.status = 'published'
)
SELECT
    p.id, p.tenant_id, p.title, p.version, p.effective_date, p.body_md,
    p.acknowledgment_required_roles, p.status, p.created_at, p.updated_at,
    p.predecessor_id, p.owner_role, p.approver_role, p.linked_control_ids,
    p.source_attribution, p.created_by, p.submitted_at, p.submitted_by,
    p.approved_at, p.approved_by, p.published_at, p.published_by,
    p.superseded_at, p.next_review_at,
    p.historical_approver_name, p.historical_approved_at, p.source_url,
    p.imported_by, p.imported_at,
    ac.ack_denominator,
    ac.ack_numerator
FROM policies p
LEFT JOIN ack_cells ac ON ac.policy_id = p.id
WHERE p.tenant_id = $1
ORDER BY p.created_at DESC, p.id ASC;

-- name: ListPolicyVersionChain :many
-- Returns the version chain for a policy id by walking predecessor_id.
-- Recursive CTE keeps the query inside Postgres rather than client-side
-- traversal. Returns oldest-first so the chain reads naturally
-- (predecessor -> successor).
WITH RECURSIVE chain AS (
    -- Anchor: the policy itself.
    SELECT *
    FROM policies p0
    WHERE p0.tenant_id = $1 AND p0.id = $2

    UNION ALL

    -- Walk forward: any row whose predecessor_id is the current chain tip.
    SELECT p.*
    FROM policies p
    INNER JOIN chain c ON p.predecessor_id = c.id AND p.tenant_id = c.tenant_id
)
SELECT chain.*
FROM chain
ORDER BY chain.created_at ASC, chain.id ASC;

-- name: SubmitPolicyForReview :one
-- Transition: draft -> under_review. Operator action; no role gate.
UPDATE policies
SET status = 'under_review',
    submitted_at = now(),
    submitted_by = $3,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'draft'
RETURNING *;

-- name: ApprovePolicy :one
-- Transition: under_review -> approved. Requires IsApprover at handler
-- (AC-4). The DB only guards the prior-state.
UPDATE policies
SET status = 'approved',
    approved_at = now(),
    approved_by = $3,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'under_review'
RETURNING *;

-- name: SupersedePolicyAtPublish :one
-- Part 1 of the two-step Publish: marks the prior 'published' row as
-- 'superseded'. Used inside the same transaction as PublishApprovedPolicy
-- (which publishes the successor row in place) so the chain transition is
-- atomic. Returns the updated prior row so the
-- handler can audit who superseded it. Only matches when the row is
-- currently 'published' (defense-in-depth -- a chain can't supersede a
-- draft).
UPDATE policies
SET status = 'superseded',
    superseded_at = now(),
    -- #1806: the successor carries its own review date; the superseded
    -- row no longer has one due.
    next_review_at = NULL,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'published'
RETURNING *;

-- name: PublishApprovedPolicy :one
-- Publishes an approved row in place. Sets status -> published, populates
-- effective_date, published_at, published_by. predecessor_id is never
-- changed: a first version keeps NULL, and a later version (a draft
-- created with predecessor_id set) keeps the link to the row that Part 1
-- just superseded (#1747). next_review_at is the review date the publisher
-- names (#1806); NULL when none is given.
UPDATE policies
SET status = 'published',
    effective_date = $3,
    published_at = now(),
    published_by = $4,
    next_review_at = $5,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'approved'
RETURNING *;

-- name: ImportPublishedPolicy :one
-- #1747 — insert an imported policy directly as 'published', carrying its
-- historical lifecycle. The in-tool submit/approve/publish transitions do
-- not apply; the handler gates the import on an admin credential instead.
-- approved_by stays NULL: the historical approver is a free-text name in
-- historical_approver_name, not a credential.
INSERT INTO policies (
    id, tenant_id, predecessor_id,
    title, version, body_md,
    owner_role, approver_role,
    linked_control_ids, acknowledgment_required_roles,
    status, effective_date, next_review_at, source_attribution,
    created_by, published_at, published_by,
    historical_approver_name, historical_approved_at, source_url,
    imported_by, imported_at
)
VALUES (
    sqlc.arg('id'), sqlc.arg('tenant_id'), NULL,
    sqlc.arg('title'), sqlc.arg('version'), sqlc.arg('body_md'),
    sqlc.arg('owner_role'), sqlc.arg('approver_role'),
    sqlc.arg('linked_control_ids'), sqlc.arg('acknowledgment_required_roles'),
    'published', sqlc.arg('effective_date'), sqlc.narg('next_review_at'), 'imported',
    sqlc.arg('imported_by'), now(), sqlc.arg('imported_by'),
    sqlc.arg('historical_approver_name'), sqlc.arg('historical_approved_at'), sqlc.narg('source_url'),
    sqlc.arg('imported_by'), now()
)
RETURNING *;

-- name: PolicyTitleVersionExists :one
-- #1747 — duplicate check for import: a policy with this title and version
-- already exists in the tenant (in any state).
SELECT EXISTS (
    SELECT 1 FROM policies
    WHERE tenant_id = $1 AND title = $2 AND version = $3
) AS exists;

-- name: InsertPolicyAuditLog :one
-- #1747 — append one row to the policy audit log.
INSERT INTO policy_audit_log (
    tenant_id, policy_id, action, actor, source_file,
    historical_approver_name, historical_approved_at, source_url,
    previous_next_review_at, new_next_review_at
)
VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('policy_id'), sqlc.arg('action'), sqlc.arg('actor'), sqlc.arg('source_file'),
    sqlc.narg('historical_approver_name'), sqlc.narg('historical_approved_at'), sqlc.narg('source_url'),
    sqlc.narg('previous_next_review_at'), sqlc.narg('new_next_review_at')
)
RETURNING *;

-- name: ListPolicyAuditLog :many
-- #1747 — the audit trail for one policy, oldest first.
SELECT *
FROM policy_audit_log
WHERE tenant_id = $1 AND policy_id = $2
ORDER BY created_at ASC, id ASC;

-- name: UpdatePolicyNextReviewAt :one
-- #1806 — change the review date of a published policy in place. It is not
-- a new version: the row keeps its id, version and approval. The caller
-- writes the policy_audit_log row in the same transaction. Only the
-- published chain tip has a review due, so other states do not match.
UPDATE policies
SET next_review_at = sqlc.narg('next_review_at'),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND status = 'published'
RETURNING *;
