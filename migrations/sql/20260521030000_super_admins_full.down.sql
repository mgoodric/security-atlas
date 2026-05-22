-- security-atlas — slice 142 down migration.
--
-- Reverses 20260521030000_super_admins_full.sql:
--   1. restores me_audit_log.action CHECK to the slice-198 16-value shape;
--   2. revokes atlas_app INSERT + DELETE on super_admins;
--   3. restores super_admins.granted_via CHECK to the slice-198 1-value shape;
--   4. drops super_admin_audit_log.

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
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export',
        'tenant_rename',
        'bootstrap_first_install'
    ));

REVOKE INSERT, DELETE ON super_admins FROM atlas_app;

ALTER TABLE super_admins
    DROP CONSTRAINT IF EXISTS super_admins_granted_via_chk;

ALTER TABLE super_admins
    ADD CONSTRAINT super_admins_granted_via_chk
    CHECK (granted_via IN ('bootstrap_first_install'));

DROP TABLE IF EXISTS super_admin_audit_log;
