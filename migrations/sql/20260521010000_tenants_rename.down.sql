-- security-atlas — slice 144 down migration.
--
-- Reverses 20260521010000_tenants_rename.sql:
--   1. restores the me_audit_log.action CHECK to its slice-181 shape;
--   2. drops the touch-trigger + function;
--   3. drops the tenants table.

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
        'anchors_export'
    ));

DROP TRIGGER IF EXISTS trg_tenants_touch_updated_at ON tenants;
DROP FUNCTION IF EXISTS tenants_touch_updated_at();

DROP TABLE IF EXISTS tenants;
