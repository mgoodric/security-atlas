-- security-atlas — slice 139 reversal.
--
-- Removes the `audit_periods_export` + `vendors_export` values from the
-- me_audit_log.action CHECK constraint, restoring the slice-135
-- five-value enum.
--
-- WARNING: applying the down migration when rows with action =
-- 'audit_periods_export' OR 'vendors_export' already exist will fail
-- the CHECK constraint. Operators rolling back to slice 135 MUST first
-- archive / delete those rows (per the standard slice-135 / 139
-- meta-audit retention policy — the rows are append-only forensics).

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified',
        'audit_log_export'
    ));
