-- Reverses 20261266000000_metric_target_audit.sql: restores the
-- me_audit_log action CHECK of 20261263000000_group_mapping_api_key_audit.sql.
--
-- Metric target audit rows are audit evidence. They are not deleted here;
-- archive and remove them deliberately first, or this refuses to apply.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM me_audit_log
     WHERE action = 'metric_target_set';
    IF n > 0 THEN
        RAISE EXCEPTION 'me_audit_log holds % metric target audit row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261266000000.';
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
        'tenant_self_approval_update',
        'tenant_vendor_initial_review_update',
        'tenant_llm_routing_update',
        'tenant_llm_routing_clear',
        'control_import_settings_update',
        'vendor_owner_update',
        'tenant_sso_config_update',
        'scim_credential_issue',
        'scim_credential_revoke',
        'group_role_mapping_create',
        'group_role_mapping_delete',
        'api_key_issue',
        'api_key_rotate',
        'api_key_revoke'
    ));
