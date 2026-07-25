-- Slice 543 — Slack + generic-webhook notification delivery channel queries.
--
-- These back two additional delivery SINKS (NOT producers, P0-543-4),
-- generalizing the slice-445 email queries. All queries are tenant-scoped
-- via the leading tenant_id; RLS under FORCE keeps the cross-tenant
-- boundary safe even on a misconfigured query (defense-in-depth).

-- name: GetSlackOptIn :one
-- Read a user's Slack-channel master opt-in. A missing row (pgx.ErrNoRows)
-- means OPTED-OUT (P0-543-3).
SELECT enabled
FROM slack_channel_optin
WHERE tenant_id = $1 AND user_id = $2;

-- name: UpsertSlackOptIn :one
-- Set a user's Slack-channel master opt-in. (tenant_id, user_id) PK is the
-- conflict target.
INSERT INTO slack_channel_optin (tenant_id, user_id, enabled, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (tenant_id, user_id)
DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()
RETURNING enabled;

-- name: GetWebhookOptIn :one
-- Read a user's webhook-channel master opt-in. Missing row = OPTED-OUT.
SELECT enabled
FROM webhook_channel_optin
WHERE tenant_id = $1 AND user_id = $2;

-- name: UpsertWebhookOptIn :one
-- Set a user's webhook-channel master opt-in.
INSERT INTO webhook_channel_optin (tenant_id, user_id, enabled, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (tenant_id, user_id)
DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()
RETURNING enabled;

-- name: ClaimChannelDigest :one
-- Idempotency claim: insert a pending delivery-log row for
-- (tenant, channel, recipient, digest_key). ON CONFLICT DO NOTHING means a
-- second claim returns no row — the caller skips the send (no double-send /
-- 24h rate-limit). The `channel` column keeps slack + webhook claims
-- independent.
INSERT INTO channel_delivery_log (
    tenant_id, channel, recipient_user_id, digest_key, outcome, attempts
)
VALUES ($1, $2, $3, $4, 'pending', 0)
ON CONFLICT (tenant_id, channel, recipient_user_id, digest_key) DO NOTHING
RETURNING id;

-- name: MarkChannelDigestSent :exec
-- Record a successful delivery. outcome=sent + sent_at + attempts++.
UPDATE channel_delivery_log
SET outcome = 'sent', sent_at = now(), attempts = attempts + 1, last_error = ''
WHERE tenant_id = $1 AND id = $2;

-- name: MarkChannelDigestFailed :exec
-- Record a failed delivery. outcome=failed + last_error + attempts++. The
-- digest_key is NOT released — the next tick can re-attempt (D8 analog).
UPDATE channel_delivery_log
SET outcome = 'failed', attempts = attempts + 1, last_error = $3
WHERE tenant_id = $1 AND id = $2;

-- name: GetChannelDeliveryLog :one
-- Read a delivery-log row by id (tests + outcome inspection).
SELECT * FROM channel_delivery_log
WHERE tenant_id = $1 AND id = $2;

-- name: ListSlackOptInUsers :many
-- Slice 582 — digest-scheduler enumeration for the Slack channel: every
-- (tenant, user) pair that has OPTED IN. Walked from the BYPASSRLS migrator
-- pool (all tenants in one pass); per-user delivery re-reads under the
-- user's own tenant GUC (RLS). enabled = false / no-row excluded (default
-- opted-OUT, P0-543-3). Returns only the (tenant, user) keys — no PII.
SELECT tenant_id, user_id
FROM slack_channel_optin
WHERE enabled = true
ORDER BY tenant_id, user_id;

-- name: ListWebhookOptInUsers :many
-- Slice 582 — digest-scheduler enumeration for the webhook channel: every
-- (tenant, user) pair that has OPTED IN. Same shape + guarantees as
-- ListSlackOptInUsers. enabled = false / no-row excluded (default
-- opted-OUT). Returns only the (tenant, user) keys — no PII.
SELECT tenant_id, user_id
FROM webhook_channel_optin
WHERE enabled = true
ORDER BY tenant_id, user_id;
