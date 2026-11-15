-- Reverses 20261116000000_vendor_initial_review_audit.sql: restores the
-- me_audit_log action CHECK of 20261031000000_self_approval.sql.
--
-- 'tenant_vendor_initial_review_update' rows are audit evidence. They are
-- not deleted here; archive and remove them deliberately first, or the
-- narrowed CHECK refuses to apply.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM me_audit_log WHERE action = 'tenant_vendor_initial_review_update';
    IF n > 0 THEN
        RAISE EXCEPTION 'me_audit_log holds % tenant_vendor_initial_review_update row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261116000000.';
    END IF;
END
$$;

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
        'tenant_self_approval_update'
    ));
