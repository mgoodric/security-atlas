-- security-atlas — notifications spine (slice 029).
--
-- New tenant-scoped table: in-app notifications. Slice 029 dispatches one
-- row per distinct prior-thread author (excluding the new note's author)
-- whenever a new audit_note lands; future slices may reuse this spine
-- for evidence-freshness alerts, policy ack reminders, etc.
--
-- Wire shape (HTTP):
--   GET   /v1/me/notifications              caller's notifications, unread first
--   PATCH /v1/me/notifications/{id}/read    mark a notification read
--
-- The payload column is JSONB so future notification types can carry
-- type-specific data without a migration. Slice 029's audit-note payload
-- shape is:
--   { "audit_note_id": "<uuid>", "scope_type": "...", "scope_id": "...",
--     "audit_period_id": "<uuid>", "author_user_id": "..." }
--
-- Constitutional invariants honored:
--   #6  Tenant isolation enforced at the DB layer via the slice-014
--       four-policy split (tenant_read FOR SELECT, tenant_write FOR INSERT
--       WITH CHECK, tenant_update FOR UPDATE USING + WITH CHECK,
--       tenant_delete FOR DELETE) under FORCE ROW LEVEL SECURITY.
--       The tenant_update policy is intentionally retained because
--       marking a notification read is an in-place UPDATE (the row is
--       not append-only -- it's a per-user read marker, not an audit
--       record).
--
-- Anti-criteria honored at the schema layer (P0):
--   - Cross-tenant leakage: tenant_id NOT NULL + 4-policy RLS under FORCE.
--   - Spam vector: there is no public write surface. Dispatch is
--     in-transaction with audit_note creation; no external API writes.
--   - Recipient impersonation: the /v1/me/notifications endpoint pins
--     recipient_user_id = caller.UserID at the query layer.
--
-- Migration is reversible via 20260511000024_notifications.down.sql.

CREATE TABLE notifications (
    id                 UUID PRIMARY KEY,
    tenant_id          UUID NOT NULL,
    recipient_user_id  TEXT NOT NULL,
    type               TEXT NOT NULL,
    payload            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at            TIMESTAMPTZ NULL,

    CONSTRAINT notifications_recipient_nonempty
        CHECK (length(recipient_user_id) > 0),
    CONSTRAINT notifications_type_nonempty
        CHECK (length(type) > 0)
);

-- Hot-path index: list-by-recipient with unread first, then newest. NULLS
-- FIRST on read_at sorts unread (NULL) ahead of read (timestamp).
CREATE INDEX idx_notifications_recipient_unread_first
    ON notifications (tenant_id, recipient_user_id, read_at NULLS FIRST, created_at DESC);

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON notifications
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON notifications
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON notifications
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON notifications
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON notifications TO atlas_app;
