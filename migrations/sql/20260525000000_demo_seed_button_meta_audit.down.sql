-- security-atlas — slice 278: demo-seed UI button meta-audit (DOWN).
--
-- Reverts both action CHECK constraints to the slice-269 baseline.
-- This drops `demo_seed`, `demo_teardown`, `demo_seed_apply`, and
-- `demo_seed_teardown` from the admitted set.
--
-- WARNING: rolling back this migration on a DB that has any rows
-- carrying the four dropped action values will FAIL the ALTER TABLE
-- ADD CONSTRAINT step (the CHECK can't be satisfied retroactively).
-- Operator should:
--
--   1. Stop any running demo-seed / atlas-cli processes.
--   2. Roll back slice 278 application code first.
--   3. Run this down migration.
--
-- If demo-seed rows exist, the down migration intentionally fails —
-- the operator must either DELETE those rows (acceptable on the
-- atlas-edge demo deployment) or skip this rollback.
--
-- Reversibility note: the forward migration restored `demo_seed_apply`
-- + `demo_seed_teardown` that slice 269 had inadvertently dropped.
-- Rolling back drops them again; if the operator subsequently rolls
-- back slice 205 too, the regression goes away.

-- ===== 1. me_audit_log.action CHECK revert =====

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
        'tenant_create'
    ));

-- ===== 2. super_admin_audit_log.action CHECK revert =====

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN (
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create'
    ));
