-- security-atlas — #2172: module access for each member (#2166, ADR 0025).
--
-- An admin narrows a member's module access to a subset of the optional
-- modules (featureflag.Modules). One row per tenant membership
-- (tenant_id, user_id): `modules` is NULL for "all", a wildcard that
-- includes modules switched on later, or an explicit set of optional-module
-- keys (possibly empty: core modules only). No row is "all", so every
-- existing and new member keeps today's behaviour with no backfill.
--
-- user_id is the user_roles key (identity.RolesKey: the bare user uuid), so
-- the access gate reads it with the same key the role resolver uses.
--
-- Module access applies to grc_engineer, control_owner and viewer only; an
-- admin always has every switched-on module and an auditor is unaffected.
-- That rule lives in the access gate (internal/moduleaccess/moduleaccess.go),
-- not here: a stored narrowed value on an admin is kept and ignored.
--
-- Constitutional invariant #6: tenant isolation by RLS. FORCE RLS with
-- SELECT / INSERT / UPDATE policies and no DELETE (setting "all" stores
-- NULL rather than deleting, so the row's history stays one key).

CREATE TABLE member_module_access (
    tenant_id   UUID        NOT NULL,
    user_id     TEXT        NOT NULL,
    -- NULL = all modules (wildcard). Otherwise the optional-module keys.
    modules     TEXT[]      NULL,
    updated_by  TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, user_id),

    CONSTRAINT member_module_access_user_id_nonempty CHECK (length(user_id) > 0),
    CONSTRAINT member_module_access_updated_by_nonempty CHECK (length(updated_by) > 0)
);

ALTER TABLE member_module_access ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_module_access FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON member_module_access
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON member_module_access
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON member_module_access
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE ON member_module_access TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON member_module_access TO atlas_migrate;

-- ===== the access audit log =====
--
-- Append-only, same shape as framework_posture_target_audit_log (#2136):
-- RLS with SELECT + INSERT policies only, and a trigger that denies UPDATE
-- and DELETE even for a role that bypasses RLS. The rows appear in the
-- unified audit log (and so Activity) as kind `module_access`. `before` and
-- `after` are the stored value: NULL for "all", else the key array.

CREATE TABLE member_module_access_audit_log (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    user_id         TEXT        NOT NULL,
    action          TEXT        NOT NULL
        CONSTRAINT member_module_access_audit_log_action_check
        CHECK (action IN ('set')),
    before          TEXT[]      NULL,
    after           TEXT[]      NULL,
    actor           TEXT        NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    subject_module  TEXT        NOT NULL DEFAULT 'core',

    CONSTRAINT member_module_access_audit_log_actor_nonempty CHECK (length(actor) > 0)
);

CREATE INDEX idx_member_module_access_audit_log_tenant_occurred
    ON member_module_access_audit_log (tenant_id, occurred_at DESC);

ALTER TABLE member_module_access_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_module_access_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON member_module_access_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON member_module_access_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON member_module_access_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION member_module_access_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'member_module_access_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER member_module_access_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON member_module_access_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION member_module_access_audit_log_append_only();
