-- Slice 439 — evidence-staleness rollup producer queries.
--
-- The rollup job (internal/staleness) reads the slice-016 freshness read
-- model per tenant, classifies each control's evidence into stale /
-- approaching / fresh bands (eval.FreshnessMaxAge owns the threshold), and
-- writes `evidence.staleness` notifications into the slice-029 notifications
-- store — one per-control alert on a threshold crossing, plus a weekly digest.
--
-- All queries are tenant-scoped via the leading tenant_id; RLS under FORCE is
-- the defense-in-depth boundary and the WHERE clause is the primary
-- correctness guarantee (canvas invariant #6). The cross-tenant leak
-- (threat-model I) is structurally prevented: the recipient enumeration runs
-- under the per-tenant GUC, and the dedup claim's WITH CHECK rejects a
-- tenant_id that does not match the GUC.

-- name: ListActiveUsersForTenant :many
-- Recipient enumeration for the staleness rollup: every ACTIVE user of the
-- tenant in ctx. Runs under the per-tenant GUC (RLS-scoped) — it returns ONLY
-- this tenant's users, so the rollup can never address a Tenant B user from a
-- Tenant A pass (threat-model I). Returns id + email; the rollup uses id as
-- the slice-029 recipient_user_id (TEXT). Ordered by id for deterministic
-- delivery + stable tests.
SELECT id, email
FROM users
WHERE tenant_id = $1
  AND status = 'active'
ORDER BY id ASC;

-- name: ClaimStalenessRollup :one
-- Idempotency claim (AC-5 / AC-12 / threat-model T). Insert one delivery-claim
-- row for (tenant, recipient, dedup_key). ON CONFLICT DO NOTHING means a
-- re-run for the same logical event returns NO row — the caller skips the
-- notification write (no duplicate alert, no double-delivered digest). The
-- WITH CHECK on tenant_write rejects a tenant_id that does not match the GUC,
-- so a mis-scoped write fails closed rather than landing in another tenant.
INSERT INTO staleness_rollup_log (
    id, tenant_id, recipient_user_id, kind, dedup_key, notification_id
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, recipient_user_id, dedup_key) DO NOTHING
RETURNING id;

-- name: GetStalenessRollupClaim :one
-- Single-claim lookup by dedup key — used by tests + idempotency assertions.
SELECT *
FROM staleness_rollup_log
WHERE tenant_id         = $1
  AND recipient_user_id = $2
  AND dedup_key         = $3;

-- name: CountStalenessRollupClaims :one
-- Count claims for a recipient (tests assert no duplicate rows after a re-run).
SELECT COUNT(*) AS claim_count
FROM staleness_rollup_log
WHERE tenant_id         = $1
  AND recipient_user_id = $2;
