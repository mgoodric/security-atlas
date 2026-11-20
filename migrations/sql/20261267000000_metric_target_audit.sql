-- security-atlas -- #2572: setting a metric's target is audited.
--
-- A metric target (target value, warning and critical thresholds,
-- direction, owner, notes) decides how a metric's tile and badge colour,
-- the same kind of setting as a framework posture target (#2136), which is
-- already audited. PUT /v1/metrics/{id}/target now writes one me_audit_log
-- row in the same transaction as the upsert, with the actor (user_id +
-- actor_principal, #2329) and the before and after value:
--
--   'metric_target_set'  PUT /v1/metrics/{id}/target
--
-- before is JSON null the first time a metric's target is set. There is no
-- delete route for a metric target, so there is no delete action.
--
-- The CHECK is rewritten in full from the latest definition
-- (20261263000000_group_mapping_api_key_audit.sql): every existing value is
-- kept and the new one is appended. internal/db/metric_target_audit_integration_test.go
-- inserts every value to prove none was dropped.
--
-- Reversible via 20261267000000_metric_target_audit.down.sql.

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
        'api_key_revoke',
        'metric_target_set'
    ));
