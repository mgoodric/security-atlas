-- Reverse #2958. The child_linked and child_unlinked rows are part of the
-- append-only trail; the trigger that guards it refuses DELETE, so the
-- down step cannot remove them and the CHECK below refuses while any exist.

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN (
        'placement_changed',
        'category_changed',
        'fields_changed',
        'treatment_changed',
        'acceptance_proposed',
        'acceptance_approved',
        'acceptance_rejected',
        'acceptance_revoked',
        'closed',
        'reopened',
        'deleted',
        'control_linked',
        'control_unlinked',
        'created',
        'themes_changed'
    ));
