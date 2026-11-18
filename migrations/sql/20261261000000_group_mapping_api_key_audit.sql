-- security-atlas -- #2609: group-role mapping create / delete and API key
-- issue / rotate / revoke are audited.
--
-- A group-role mapping decides which IdP group grants which atlas role, and
-- an API key is a standing credential; an organisation admin can change
-- both. Each change writes one me_audit_log row in the same transaction as
-- the change, with the actor (user_id + actor_principal) and the before and
-- after value:
--
--   'group_role_mapping_create'  POST   /v1/admin/group-role-mappings
--   'group_role_mapping_delete'  DELETE /v1/admin/group-role-mappings/{id}
--   'api_key_issue'              POST   /v1/admin/credentials
--   'api_key_rotate'             POST   /v1/admin/credentials/{id}/rotate
--   'api_key_revoke'             POST   /v1/admin/credentials/{id}/revoke
--
-- A key is recorded by id, last4, flags and roles: never its token or token
-- hash.
--
-- The CHECK is rewritten in full from the latest definition
-- (20261260000000_sso_scim_audit.sql): every existing value is kept and the
-- new ones are appended. internal/db/group_mapping_api_key_audit_integration_test.go
-- inserts every value to prove none was dropped.
--
-- Reversible via 20261261000000_group_mapping_api_key_audit.down.sql.

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
