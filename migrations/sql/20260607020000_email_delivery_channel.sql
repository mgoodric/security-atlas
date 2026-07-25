-- security-atlas — slice 445: email/SMTP notification delivery channel.
--
-- Two tenant-scoped tables backing the email delivery substrate. This
-- slice is a DELIVERY SINK for the slice-029 notifications store; it does
-- NOT produce notifications (P0-445-5).
--
--   email_channel_optin   — per-user master opt-in toggle (AC-9). Default
--                           OPTED-OUT (P0-445-7): a user with NO row reads
--                           as opted-out. This is the inverse of the
--                           slice-108 user_notification_preferences
--                           default-ON policy, which is correct: the
--                           lower-trust email channel defaults off.
--
--   email_delivery_log    — idempotency + outcome ledger (AC-5 / AC-8).
--                           A UNIQUE (tenant_id, recipient_user_id,
--                           digest_key) constraint serializes the
--                           claim-before-send so a digest is never
--                           double-sent; the per-UTC-day digest_key is
--                           also the 24h rate-limit (D5/D6).
--
-- JUDGMENT decisions: see docs/audit-log/445-email-channel-decisions.md.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation via RLS on every new tenant-scoped table:
--       canonical four-policy split (read/write/update/delete) under
--       ENABLE + FORCE ROW LEVEL SECURITY. Delivery reads notifications
--       under the notification's OWN tenant GUC; cross-tenant email is
--       proven absent (AC-13).
--
-- Reversible via 20260607020000_email_delivery_channel.down.sql.

-- ===== email_channel_optin =====
--
-- One row per (tenant, user). enabled DEFAULT false means an explicit
-- INSERT/UPSERT is required to opt in; a fresh user (no row) is opted-out
-- (P0-445-7). The user_id FK CASCADE-deletes the opt-in when the user is
-- removed.
CREATE TABLE email_channel_optin (
    tenant_id   UUID NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT email_channel_optin_pk PRIMARY KEY (tenant_id, user_id)
);

ALTER TABLE email_channel_optin ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_channel_optin FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON email_channel_optin
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON email_channel_optin
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON email_channel_optin
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON email_channel_optin
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== email_delivery_log =====
--
-- The delivery ledger. The UNIQUE (tenant_id, recipient_user_id,
-- digest_key) is the idempotency key (AC-5): the channel claims it with
-- INSERT ... ON CONFLICT DO NOTHING before sending. outcome records
-- sent/failed (AC-8); attempts + last_error support backoff (D8).
--
-- recipient_user_id is the slice-029 string user-id shape (matches
-- notifications.recipient_user_id). The recipient EMAIL is resolved
-- server-side at send time from users.email and is NOT stored here (no
-- need to persist PII in the log).
CREATE TABLE email_delivery_log (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL,
    recipient_user_id TEXT NOT NULL,
    digest_key        TEXT NOT NULL,
    outcome           TEXT NOT NULL DEFAULT 'pending'
                      CHECK (outcome IN ('pending', 'sent', 'failed')),
    attempts          INTEGER NOT NULL DEFAULT 0,
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at           TIMESTAMPTZ,
    CONSTRAINT email_delivery_log_idem
        UNIQUE (tenant_id, recipient_user_id, digest_key)
);

CREATE INDEX email_delivery_log_tenant_created
    ON email_delivery_log (tenant_id, created_at DESC);

ALTER TABLE email_delivery_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_delivery_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON email_delivery_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON email_delivery_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON email_delivery_log
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON email_delivery_log
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON email_channel_optin TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON email_channel_optin TO atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON email_delivery_log TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON email_delivery_log TO atlas_migrate;
