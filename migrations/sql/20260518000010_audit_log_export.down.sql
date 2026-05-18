-- security-atlas — slice 135 reversal.
--
-- Removes the `audit_log_export` value from the me_audit_log.action
-- CHECK constraint, restoring the slice-124 four-value enum.

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified'
    ));
