-- Reverse slice 174: drop the `anchors_export` action value from the
-- `me_audit_log.action` CHECK constraint. Restores the post-slice-138
-- baseline (which includes the `*_export` values added through
-- slice 138).
--
-- Reapplying the forward migration is idempotent (ALTER ... DROP IF
-- EXISTS + ADD); reapplying this down migration is idempotent for the
-- same reason.

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
        'samples_export'
    ));
