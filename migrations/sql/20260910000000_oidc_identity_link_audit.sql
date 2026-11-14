-- #1944: audit the OIDC email link.
--
-- A SCIM-provisioned user's first OIDC sign-in binds the IdP principal
-- (idp_issuer, idp_subject) to the SCIM row by email. That binding decides
-- who can sign in as the user from then on, so UpsertOIDC writes one
-- me_audit_log row per link: user_id is the linked user, `after` carries
-- idp_issuer + idp_config_id (no email). This adds the 'oidc_identity_link'
-- action to the allow-list; the list below mirrors the slice-608 form
-- (20260608030000) plus the new value.

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
        -- #1944 (OIDC sign-in linked to a SCIM-provisioned user by email):
        'oidc_identity_link'
    ));
