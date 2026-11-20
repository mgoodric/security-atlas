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
-- Rate denominator (#2586): the distinct active users holding a live
-- user_roles row the policy's audience names, or any live role when the
-- audience is 'everyone'. This is the set the authz engine resolves
-- (authz.DBRolesResolver reads the same rows and gives a user whose
-- users.status is not 'active' none) and the set policy.WithEveryone admits,
-- so the denominator is who may acknowledge. user_roles.user_id is TEXT; a
-- "user:<uuid>" principal is read as its bare uuid, and a row naming no
-- users row (a key, bootstrap-admin) is not a person and is not counted.
--
-- Before #2586 this counted api_keys holders: a user whose role was revoked
-- but whose key was not was counted, and a JWT-only member was missed.
-- ListPoliciesWithAckRate (policies.sql) repeats this predicate; keep them
-- in sync.
SELECT COUNT(DISTINCT u.id)::bigint AS count
FROM user_roles ur
JOIN users u
  ON u.tenant_id = ur.tenant_id
 AND u.id::text  = regexp_replace(ur.user_id, '^user:', '')
WHERE ur.tenant_id = $1
  AND u.status = 'active'
  AND (
      ur.role = ANY(sqlc.arg('required_roles')::text[])
      OR 'everyone' = ANY(sqlc.arg('required_roles')::text[])
  );

-- name: CountFreshAcksForVersion :one
-- Rate numerator: distinct user_ids who (a) are in the denominator set
-- per the same live-role predicate AND (b) have at least one ack of
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
      FROM user_roles ur
      JOIN users u
        ON u.tenant_id = ur.tenant_id
       AND u.id::text  = regexp_replace(ur.user_id, '^user:', '')
      WHERE ur.tenant_id = pa.tenant_id
        AND u.id = pa.user_id
        AND u.status = 'active'
        AND (
            ur.role = ANY(sqlc.arg('required_roles')::text[])
            OR 'everyone' = ANY(sqlc.arg('required_roles')::text[])
        )
  );
