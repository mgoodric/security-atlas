-- Reverse #2104. The fields_changed rows are part of the append-only
-- trail; the trigger that guards it refuses DELETE, so the down step
-- cannot remove them and refuses while any exist.

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN ('placement_changed', 'category_changed'));
