-- security-atlas — slice 378 reverse migration.
--
-- Reverts the action CHECK constraints on `me_audit_log` and
-- `super_admin_audit_log` to the slice-278 baseline by dropping
-- `authz_bundle_reload` from the admitted set.
--
-- Reversal is safe because:
--   1. No rows tagged `action='authz_bundle_reload'` should exist if
--      slice 378 was rolled back before any reload was performed; in
--      a forward-roll-back-roll-forward scenario the operator must
--      drop those rows manually (audit-log rows are not orphaned by
--      schema change, only by deletion).
--   2. Append-only ledger contract is preserved: this down-migration
--      tightens the CHECK; it does not modify any existing row.

-- ===== 1. me_audit_log.action CHECK restore =====

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
        'demo_teardown'
    ));

-- ===== 2. super_admin_audit_log.action CHECK restore =====

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN (
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown'
    ));
