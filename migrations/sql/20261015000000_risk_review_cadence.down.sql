-- Reverse #2059: restore the me_audit_log.action allow-list to the #1944 form
-- (20260920000000), drop the review-due default trigger and the cadence
-- column.
--
-- The backfilled review_due_at values stay: they are indistinguishable from
-- dates a user set, and a schema down-migration does not delete data. Any
-- 'tenant_risk_review_cadence_update' audit rows written before rollback are
-- removed by the operator manually, as with the slice-608 down migration.

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
        'oidc_identity_link'
    ));

DROP TRIGGER IF EXISTS risks_review_due_default_trg ON risks;
DROP FUNCTION IF EXISTS risks_review_due_default();

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_risk_review_cadence_days_chk;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS risk_review_cadence_days;
