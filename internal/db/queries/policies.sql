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
-- Publish is a two-step operation (supersede prior + insert new); the
-- application wraps both queries in a single tx so the chain is atomic.

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
-- 'superseded'. Used inside the same transaction as InsertPublishedPolicy
-- so the chain transition is atomic. Returns the updated prior row so the
-- handler can audit who superseded it. Only matches when the row is
-- currently 'published' (defense-in-depth -- a chain can't supersede a
-- draft).
UPDATE policies
SET status = 'superseded',
    superseded_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'published'
RETURNING *;

-- name: PublishApprovedPolicy :one
-- One-shot publish of an approved row that has NO predecessor (the very
-- first version). Sets status -> published, populates effective_date,
-- published_at, published_by. Predecessor_id stays NULL.
UPDATE policies
SET status = 'published',
    effective_date = $3,
    published_at = now(),
    published_by = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'approved'
RETURNING *;

-- name: InsertPublishedPolicy :one
-- Part 2 of the two-step Publish for SECOND-and-later versions: clones
-- the approved row's content into a NEW row with status='published',
-- predecessor_id pointing at the just-superseded prior. Called inside
-- the same tx as SupersedePolicyAtPublish so the version chain stays
-- atomic. The caller supplies the new row's id and version string.
INSERT INTO policies (
    id, tenant_id, predecessor_id,
    title, version, body_md,
    owner_role, approver_role,
    linked_control_ids, acknowledgment_required_roles,
    status, effective_date, source_attribution,
    created_by, published_at, published_by
)
VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, $8,
    $9, $10,
    'published', $11, $12,
    $13, now(), $14
)
RETURNING *;
