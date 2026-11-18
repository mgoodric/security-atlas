-- security-atlas -- #2450: an append-only audit trail for vendor owner
-- changes, read through the unified audit log as kind `vendor`.
--
-- #2129 wrote each vendor owner change to me_audit_log
-- ('vendor_owner_update'). That is the per-user log: the payload gate
-- (#2379) shows a me row's before/after only to its actor or an admin, and
-- a caller that is not a user was recorded as uuid.Nil. Owner changes are a
-- vendor's record, so they belong in a vendor log whose payload follows the
-- vendor read rule, as risk changes sit in risk_audit_log (#1749).
--
-- New owner changes are written here. Existing 'vendor_owner_update' rows
-- stay in me_audit_log: moving audit evidence is a separate, approved step,
-- so the me_audit_log CHECK keeps accepting the action.
--
-- Shape follows risk_audit_log: RLS with SELECT + INSERT policies only,
-- plus a trigger that denies UPDATE and DELETE even for a role that
-- bypasses RLS. No FK to vendors: the trail outlives the vendor.

CREATE TABLE vendor_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    vendor_id    UUID NOT NULL,
    -- The caller's principal (credstore.PrincipalOf, #2329): the bare
    -- users(id) uuid for a person, else the key or client id. Never a
    -- token, and never anonymous.
    actor        TEXT NOT NULL,
    action_type  TEXT NOT NULL
                 CONSTRAINT vendor_audit_log_action_type_check
                 CHECK (action_type IN ('owner_changed')),
    before_state JSONB NOT NULL,
    after_state  JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Slice 180 convention for the audit-log family.
    subject_module TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT vendor_audit_log_actor_nonempty CHECK (length(actor) BETWEEN 1 AND 256)
);

CREATE INDEX vendor_audit_log_tenant_vendor
    ON vendor_audit_log (tenant_id, vendor_id, created_at DESC);

ALTER TABLE vendor_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_audit_log FORCE ROW LEVEL SECURITY;

-- Append-only by RLS construction: SELECT + INSERT policies only.
CREATE POLICY tenant_read ON vendor_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON vendor_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON vendor_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION vendor_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'vendor_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER vendor_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON vendor_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION vendor_audit_log_append_only();
