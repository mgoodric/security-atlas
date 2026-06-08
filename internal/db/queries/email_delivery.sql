-- Slice 445 — email/SMTP notification delivery channel queries.
--
-- These back the email delivery substrate (a SINK for slice-029
-- notifications, NOT a producer). All queries are tenant-scoped via the
-- leading tenant_id; RLS under FORCE keeps the cross-tenant boundary safe
-- even on a misconfigured query (defense-in-depth on top of RLS).

-- name: GetEmailOptIn :one
-- Read a user's email-channel master opt-in. A missing row (pgx.ErrNoRows)
-- means OPTED-OUT (P0-445-7) — the application layer treats no-row as
-- enabled=false.
SELECT enabled
FROM email_channel_optin
WHERE tenant_id = $1 AND user_id = $2;

-- name: UpsertEmailOptIn :one
-- Set a user's email-channel master opt-in (AC-9). The (tenant_id,
-- user_id) PK is the conflict target.
INSERT INTO email_channel_optin (tenant_id, user_id, enabled, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (tenant_id, user_id)
DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()
RETURNING enabled;

-- name: ClaimEmailDigest :one
-- Idempotency claim (AC-5): insert a pending delivery-log row for
-- (tenant, recipient, digest_key). ON CONFLICT DO NOTHING means a
-- second claim for the same digest returns no row — the caller skips the
-- send (no double-send / 24h rate-limit). Returns the claimed row id when
-- the claim succeeds.
INSERT INTO email_delivery_log (
    tenant_id, recipient_user_id, digest_key, outcome, attempts
)
VALUES ($1, $2, $3, 'pending', 0)
ON CONFLICT (tenant_id, recipient_user_id, digest_key) DO NOTHING
RETURNING id;

-- name: MarkEmailDigestSent :exec
-- Record a successful delivery (AC-8). Sets outcome=sent + sent_at +
-- increments attempts.
UPDATE email_delivery_log
SET outcome = 'sent', sent_at = now(), attempts = attempts + 1, last_error = ''
WHERE tenant_id = $1 AND id = $2;

-- name: MarkEmailDigestFailed :exec
-- Record a failed delivery (AC-8). Sets outcome=failed + last_error +
-- increments attempts. The digest_key is NOT released — the next tick
-- can re-attempt by reading attempts for a backoff decision (D8); a
-- dedicated re-claim/backoff scheduler is a follow-on.
UPDATE email_delivery_log
SET outcome = 'failed', attempts = attempts + 1, last_error = $3
WHERE tenant_id = $1 AND id = $2;

-- name: GetEmailDeliveryLog :one
-- Read a delivery-log row by id (tests + outcome inspection).
SELECT * FROM email_delivery_log
WHERE tenant_id = $1 AND id = $2;

-- name: ListEmailOptInUsers :many
-- Slice 582 — the digest scheduler's enumeration query for the email
-- channel: every (tenant, user) pair that has OPTED IN. Runs through the
-- BYPASSRLS migrator pool so the scheduler can walk all tenants in one
-- pass; the actual per-user delivery then re-reads under the user's own
-- tenant GUC (RLS). enabled = false / no-row are excluded (default
-- opted-OUT, P0-445-7). No PII is returned — only the (tenant, user) keys
-- the driver needs to call DeliverDigest.
SELECT tenant_id, user_id
FROM email_channel_optin
WHERE enabled = true
ORDER BY tenant_id, user_id;
