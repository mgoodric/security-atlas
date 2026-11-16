-- Reverses 20261226000000_risk_vendor_owner_users.sql: drops
-- risks.owner_user_id and vendors.owner_user_id, and restores the
-- me_audit_log action CHECK of 20261120000000_llm_settings_audit.sql.
--
-- The up migration never changed the free text, so every record it linked
-- keeps its original treatment_owner / owner_user text and is restored
-- exactly. A record whose owner was picked after the migration has its text
-- cleared by the edit; before the column goes, that text is set to the
-- owner's name (or email), so the owner is not lost. Text that is already
-- set is never overwritten.
--
-- 'vendor_owner_update' rows are audit evidence. They are not deleted here;
-- archive and remove them deliberately first, or the narrowed CHECK refuses
-- to apply.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM me_audit_log WHERE action = 'vendor_owner_update';
    IF n > 0 THEN
        RAISE EXCEPTION 'me_audit_log holds % vendor owner audit row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261226000000.';
    END IF;
END
$$;

UPDATE risks r
   SET treatment_owner = COALESCE(NULLIF(btrim(u.display_name), ''), u.email)
  FROM users u
 WHERE u.id = r.owner_user_id
   AND u.tenant_id = r.tenant_id
   AND btrim(r.treatment_owner) = '';

UPDATE vendors v
   SET owner_user = COALESCE(NULLIF(btrim(u.display_name), ''), u.email)
  FROM users u
 WHERE u.id = v.owner_user_id
   AND u.tenant_id = v.tenant_id
   AND btrim(v.owner_user) = '';

DROP INDEX IF EXISTS risks_owner_user;
DROP INDEX IF EXISTS vendors_owner_user;
ALTER TABLE risks DROP COLUMN IF EXISTS owner_user_id;
ALTER TABLE vendors DROP COLUMN IF EXISTS owner_user_id;

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified',
        'audit_log_export',
        'audit_periods_export',
        'vendors_export',
        'risk_export',
        'controls_export',
        'controls_history_export',
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export',
        'dashboard_export',
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown',
        'authz_bundle_reload',
        'user_tenant_assign',
        'user_tenant_revoke',
        'tenant_gate_policy_update',
        'oidc_identity_link',
        'tenant_risk_review_cadence_update',
        'tenant_self_approval_update',
        'tenant_vendor_initial_review_update',
        'tenant_llm_routing_update',
        'tenant_llm_routing_clear',
        'control_import_settings_update'
    ));
