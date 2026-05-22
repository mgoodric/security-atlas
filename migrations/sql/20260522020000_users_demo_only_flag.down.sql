-- security-atlas — slice 205: demo seed dataset (DOWN).
--
-- Reverses 20260522020000_users_demo_only_flag.sql by:
--
--   1. Restoring `me_audit_log.action` CHECK to the slice-175 shape
--      (dropping 'demo_seed_apply' + 'demo_seed_teardown').
--   2. Restoring `super_admin_audit_log.action` CHECK to the slice-143
--      three-value shape.
--   3. Dropping `users.demo_only`.
--
-- WARNING: applying this down migration when rows with demo_only=TRUE
-- or audit-log rows with action='demo_seed_*' exist will succeed for
-- the column drop but will fail for the CHECK restore (existing rows
-- violate the narrowed CHECK). Operator must teardown demo tenants
-- (atlas-cli demo teardown) and purge audit-log rows manually before
-- applying this down migration.

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
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create'
    ));

-- ===== 2. super_admin_audit_log.action CHECK restore =====

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN (
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create'
    ));

-- ===== 3. users.demo_only column =====

ALTER TABLE users
    DROP COLUMN IF EXISTS demo_only;
