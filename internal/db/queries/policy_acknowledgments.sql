-- Slice 023 — policy acknowledgment queries.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee. The 365-day freshness window is parameterized so tests can
-- shift the boundary via store.WithClock without rewriting SQL.

-- name: InsertPolicyAcknowledgment :one
-- Append-only insert. The ack_token's UNIQUE partial index dedups
-- double-clicks within the same UTC day; the handler treats the
-- 23505 violation as "deduplicated, return original".
INSERT INTO policy_acknowledgments (
    id, tenant_id, policy_id, policy_version_id, user_id,
    acknowledged_at, ack_token, evidence_record_id
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8
)
RETURNING *;

-- name: GetAcknowledgmentByToken :one
-- Idempotency lookup for the handler's deduplicate path.
SELECT *
FROM policy_acknowledgments
WHERE tenant_id = $1
  AND ack_token = sqlc.arg('ack_token')::text;

-- name: SetAcknowledgmentEvidenceRecord :exec
-- Backreference the evidence record id after slice-013 Process succeeds.
-- Same-tx update so the row never observes the partial state from
-- outside.
UPDATE policy_acknowledgments
SET evidence_record_id = $3
WHERE tenant_id = $1
  AND id = $2;

-- name: ListPendingAcksForUser :many
-- One-shot query returning every published policy the user must
-- acknowledge plus their most-recent ack timestamp (if any). Replaces
-- the slice-023 N+1 (one ListPublishedPolicies + N
-- LatestAcknowledgmentForUserAndVersion calls) with a single LEFT JOIN.
--
-- A row appears in the result when:
--   1. status = 'published' AND cardinality(acknowledgment_required_roles) > 0
--   2. The caller's roles intersect required_roles, OR caller is admin.
--   3. (no fresh ack exists) computed in Go: we return latest_ack_at and
--      let the handler compare against the freshness cutoff. Pushing
--      the cutoff into SQL too would mask "stale" from "never ack'd"
--      in the response payload, which the UI uses to label items.
--
-- Sort by title ASC, created_at DESC for deterministic ordering.
SELECT
    p.id,
    p.title,
    p.version,
    p.effective_date,
    p.acknowledgment_required_roles,
    pa.acknowledged_at AS latest_ack_at
FROM policies p
LEFT JOIN LATERAL (
    SELECT acknowledged_at
    FROM policy_acknowledgments
    WHERE tenant_id = p.tenant_id
      AND user_id = $2
      AND policy_version_id = p.id
    ORDER BY acknowledged_at DESC
    LIMIT 1
) pa ON true
WHERE p.tenant_id = $1
  AND p.status = 'published'
  AND cardinality(p.acknowledgment_required_roles) > 0
  AND (
      sqlc.arg('is_admin')::boolean
      OR p.acknowledgment_required_roles && sqlc.arg('owner_roles')::text[]
  )
ORDER BY p.title ASC, p.created_at DESC;

-- name: GetPolicyForAcknowledge :one
-- Single-row lookup used by POST /v1/policies/{id}/acknowledge. The
-- handler checks status='published' itself so it can return a precise
-- 409 (vs 404) when the row exists but is not currently in force.
SELECT *
FROM policies
WHERE tenant_id = $1 AND id = $2;

-- name: CountRequiredRoleUsersForVersion :one
-- Rate denominator: distinct user_ids in api_keys whose owner_roles
-- intersect the policy's acknowledgment_required_roles OR are flagged
-- is_admin (admin wildcard). Excludes revoked keys. A user with two
-- credentials counts once.
--
-- slice-035 (OPA-RBAC) graduates this: replace api_keys with a proper
-- user-role binding table. Until then this is the stand-in per CONTEXT.md
-- "Policy acknowledgment (slice 023)".
SELECT COUNT(DISTINCT k.issued_by)::bigint AS count
FROM api_keys k
WHERE k.tenant_id = $1
  AND k.revoked_at IS NULL
  AND k.issued_by IS NOT NULL
  AND (
      k.is_admin = true
      OR k.owner_roles && sqlc.arg('required_roles')::text[]
  );

-- name: CountFreshAcksForVersion :one
-- Rate numerator: distinct user_ids who (a) are in the denominator set
-- per the same role predicate AND (b) have at least one ack of
-- policy_version_id with acknowledged_at >= the freshness cutoff.
--
-- The cutoff is a parameter (not `now() - interval '365 days'`) so tests
-- inject via store.WithClock and integration tests aren't time-bombed.
SELECT COUNT(DISTINCT pa.user_id)::bigint AS count
FROM policy_acknowledgments pa
WHERE pa.tenant_id = $1
  AND pa.policy_version_id = $2
  AND pa.acknowledged_at >= sqlc.arg('freshness_cutoff')::timestamptz
  AND EXISTS (
      SELECT 1
      FROM api_keys k
      WHERE k.tenant_id = pa.tenant_id
        AND k.issued_by = pa.user_id
        AND k.revoked_at IS NULL
        AND (
            k.is_admin = true
            OR k.owner_roles && sqlc.arg('required_roles')::text[]
        )
  );
