-- Reverse #2118. audit_period_audit_log is append-only (no DELETE policy),
-- so the auditor_assigned / auditor_removed rows stay, and the down step
-- refuses while any exist.

ALTER TABLE audit_period_audit_log
    DROP CONSTRAINT audit_period_audit_log_action_chk;

ALTER TABLE audit_period_audit_log
    ADD CONSTRAINT audit_period_audit_log_action_chk
    CHECK (action IN (
        'period_created',
        'period_frozen',
        'freeze_rejected_already_frozen',
        'population_attached'
    ));
