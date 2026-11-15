-- #2201: the vendor initial-review window is settable through
-- PATCH /v1/tenants/{id} and the Settings page, and each change is audited.
--
-- tenants.vendor_initial_review_days (#2061, 20261018000000) could only be
-- changed in the database. The handler now sets it beside
-- risk_review_cadence_days and writes one me_audit_log row per change, with
-- action 'tenant_vendor_initial_review_update'.
--
-- The CHECK is rewritten in full from the latest definition
-- (20261031000000_self_approval.sql): every existing value is kept and the
-- new one is appended. internal/db/vendor_initial_review_audit_integration_test.go
-- inserts every value to prove none was dropped.
--
-- Reversible via 20261116000000_vendor_initial_review_audit.down.sql.

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
        'tenant_vendor_initial_review_update'
    ));
