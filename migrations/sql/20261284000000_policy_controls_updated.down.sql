-- security-atlas -- reverse 20261284000000_policy_controls_updated.sql.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'controls_updated' row exists, or any row carries a
-- link list: the operator archives and removes them deliberately first.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log
    WHERE action = 'controls_updated'
       OR previous_linked_control_ids IS NOT NULL
       OR new_linked_control_ids IS NOT NULL;
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % controls_updated row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261284000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    DROP COLUMN IF EXISTS previous_linked_control_ids,
    DROP COLUMN IF EXISTS new_linked_control_ids;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped', 'synced'));
