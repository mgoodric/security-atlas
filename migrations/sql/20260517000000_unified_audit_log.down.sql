-- security-atlas — slice 124 reversal.
--
-- Removes:
--   1. `idx_aggregation_rule_audit_log_tenant_created`
--   2. The extended `me_audit_log.action` CHECK constraint, restoring the
--      slice-108 three-value enum.

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke'
    ));

DROP INDEX IF EXISTS idx_aggregation_rule_audit_log_tenant_created;
