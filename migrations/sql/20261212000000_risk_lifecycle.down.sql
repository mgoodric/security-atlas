-- Reverse #2107. The lifecycle rows are part of the append-only trail; the
-- trigger that guards it refuses DELETE, so the down step cannot remove
-- them and the CHECK below refuses while any exist.

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
        'acceptance_revoked'
    ));

ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_closure_note_length;
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_closure_consistent;
ALTER TABLE risks DROP COLUMN IF EXISTS closed_by;
ALTER TABLE risks DROP COLUMN IF EXISTS closed_at;
ALTER TABLE risks DROP COLUMN IF EXISTS closure_note;
ALTER TABLE risks DROP COLUMN IF EXISTS closure_reason;
ALTER TABLE risks DROP COLUMN IF EXISTS status;
DROP TYPE IF EXISTS risk_closure_reason;
DROP TYPE IF EXISTS risk_status;
