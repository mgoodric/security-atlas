-- security-atlas — slice 543: Slack + generic-webhook notification channels.
--
-- Generalizes the slice-445 email delivery substrate to two more delivery
-- SINKS. Each channel reuses the exact slice-445 shape: a per-user master
-- opt-in (default OPTED-OUT, P0-543-3) and an idempotency + outcome ledger
-- keyed by a per-UTC-day digest_key (the claim-before-send guard, the 24h
-- rate-limit). These are SINKS for the slice-029 notifications store; they
-- do NOT produce notifications (P0-543-4).
--
-- The per-channel target (Slack channel id / webhook URL) and credentials
-- are OPERATOR-configured (env), NOT stored per-user and NEVER
-- user-controlled free-text (P0-543-2 / threat-model S). That is why these
-- tables carry only the opt-in flag + the delivery ledger — there is no
-- user-supplied target column to abuse.
--
-- JUDGMENT decisions: see docs/audit-log/543-additional-channels-decisions.md.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation via RLS on every new tenant-scoped table:
--       canonical four-policy split (read/write/update/delete) under
--       ENABLE + FORCE ROW LEVEL SECURITY. Delivery reads notifications
--       under the notification's OWN tenant GUC; cross-tenant delivery is
--       proven absent (AC integration test).
--
-- Reversible via 20260608000000_slack_webhook_channels.down.sql.

-- ===== slack_channel_optin =====
CREATE TABLE slack_channel_optin (
    tenant_id   UUID NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT slack_channel_optin_pk PRIMARY KEY (tenant_id, user_id)
);

ALTER TABLE slack_channel_optin ENABLE ROW LEVEL SECURITY;
ALTER TABLE slack_channel_optin FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON slack_channel_optin
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON slack_channel_optin
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON slack_channel_optin
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON slack_channel_optin
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== webhook_channel_optin =====
CREATE TABLE webhook_channel_optin (
    tenant_id   UUID NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT webhook_channel_optin_pk PRIMARY KEY (tenant_id, user_id)
);

ALTER TABLE webhook_channel_optin ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_channel_optin FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON webhook_channel_optin
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON webhook_channel_optin
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON webhook_channel_optin
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON webhook_channel_optin
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== channel_delivery_log =====
--
-- A SINGLE ledger for the non-email channels, discriminated by `channel`.
-- The UNIQUE (tenant_id, channel, recipient_user_id, digest_key) is the
-- idempotency key: the channel claims it with INSERT ... ON CONFLICT DO
-- NOTHING before delivering. The `channel` column in the key keeps the
-- slack + webhook claims independent of each other and of email (which has
-- its own slice-445 table). No PII / notification content is stored here.
CREATE TABLE channel_delivery_log (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL,
    channel           TEXT NOT NULL CHECK (channel IN ('slack', 'webhook')),
    recipient_user_id TEXT NOT NULL,
    digest_key        TEXT NOT NULL,
    outcome           TEXT NOT NULL DEFAULT 'pending'
                      CHECK (outcome IN ('pending', 'sent', 'failed')),
    attempts          INTEGER NOT NULL DEFAULT 0,
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at           TIMESTAMPTZ,
    CONSTRAINT channel_delivery_log_idem
        UNIQUE (tenant_id, channel, recipient_user_id, digest_key)
);

CREATE INDEX channel_delivery_log_tenant_created
    ON channel_delivery_log (tenant_id, created_at DESC);

ALTER TABLE channel_delivery_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_delivery_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON channel_delivery_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON channel_delivery_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON channel_delivery_log
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON channel_delivery_log
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON slack_channel_optin TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON slack_channel_optin TO atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON webhook_channel_optin TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON webhook_channel_optin TO atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON channel_delivery_log TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON channel_delivery_log TO atlas_migrate;
