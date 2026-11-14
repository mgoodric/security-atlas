-- Reverse #1806: narrow policy_audit_log back to the import action.
--
-- The append-only trigger refuses DELETE, so it is disabled for the one
-- statement that drops the review-date rows the narrowed CHECK would
-- reject, then re-enabled.

ALTER TABLE policy_audit_log DISABLE TRIGGER policy_audit_log_no_update_trg;
DELETE FROM policy_audit_log WHERE action = 'review_date_updated';
ALTER TABLE policy_audit_log ENABLE TRIGGER policy_audit_log_no_update_trg;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported')),
    DROP COLUMN IF EXISTS new_next_review_at,
    DROP COLUMN IF EXISTS previous_next_review_at;
