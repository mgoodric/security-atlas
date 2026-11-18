-- security-atlas -- #2609: SSO config changes and SCIM credential issue /
-- revoke are audited.
--
-- The tenant's SSO config decides who can sign in, and a SCIM credential
-- provisions its users; since #2632 / #2636 an organisation admin can change
-- both. Each change writes one me_audit_log row in the same transaction as
-- the change, with the actor (user_id + actor_principal) and the before and
-- after value:
--
--   'tenant_sso_config_update'   PATCH  /v1/admin/sso
--   'scim_credential_issue'      POST   /v1/admin/scim-credentials
--   'scim_credential_revoke'     DELETE /v1/admin/scim-credentials/{id}
--
-- The SSO client secret is recorded only as secret_changed true/false: never
-- its value, ciphertext, hash or length. A SCIM credential is recorded by id
-- and last4: never its token or token hash.
--
-- The CHECK is rewritten in full from the latest definition
-- (20261226000000_risk_vendor_owner_users.sql): every existing value is kept
-- and the new ones are appended. internal/db/sso_scim_audit_integration_test.go
-- inserts every value to prove none was dropped.
--
-- Reversible via 20261260000000_sso_scim_audit.down.sql.

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
        'scim_credential_revoke'
    ));
