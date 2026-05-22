-- security-atlas — slice 198 down migration.
--
-- Reverses 20260521020000_super_admins.sql:
--   1. restores the me_audit_log.action CHECK to the slice-144 shape (14 values);
--   2. drops the super_admins table.

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
        'tenant_rename'
    ));

DROP TABLE IF EXISTS super_admins;
