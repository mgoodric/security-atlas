-- Reverse #2105. The transition rows are part of the append-only trail;
-- the trigger that guards it refuses DELETE, so the down step cannot remove
-- them and the CHECK below refuses while any exist.

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN ('placement_changed', 'category_changed', 'fields_changed'));

DROP TRIGGER IF EXISTS risks_self_approval_requires_setting_trg ON risks;
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_self_approved_is_approved;
ALTER TABLE risks DROP COLUMN IF EXISTS self_approved;
ALTER TABLE risks DROP COLUMN IF EXISTS acceptance_approved_at;
ALTER TABLE risks DROP COLUMN IF EXISTS acceptance_proposed_at;
ALTER TABLE risks DROP COLUMN IF EXISTS acceptance_proposed_by;
ALTER TABLE risks DROP COLUMN IF EXISTS acceptance_proposed_until;
ALTER TABLE risks DROP COLUMN IF EXISTS acceptance_justification;
