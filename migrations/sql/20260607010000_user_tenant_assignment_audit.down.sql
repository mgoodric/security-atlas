-- security-atlas — slice 478 reverse migration.
--
-- Reverts the action CHECK constraints on `me_audit_log` and
-- `super_admin_audit_log` to the slice-378 baseline by dropping
-- 'user_tenant_assign' + 'user_tenant_revoke' from the admitted set.
--
-- Reversal is safe because:
--   1. No rows tagged with the two slice-478 actions should exist if slice
--      478 was rolled back before any assign/revoke; in a forward-rollback-
--      forward scenario the operator drops those rows manually (audit rows
--      are not orphaned by schema change, only by deletion).
--   2. Append-only ledger contract preserved: this down-migration tightens
--      the CHECK; it does not modify any existing row.
--
-- NOTE: the local-auth synthetic IdP tuples written into `users` by the
-- slice-478 handler (D1) are pure DATA and are NOT reverted here — they remain
-- valid `users` rows under the unchanged users_idp_principal_unique index. A
-- full slice-478 data rollback (un-backfilling synthetic tuples) is out of
-- scope for a schema down-migration and would be a manual operator task.

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
        'demo_teardown',
        'authz_bundle_reload'
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
        'demo_teardown',
        'authz_bundle_reload'
    ));
