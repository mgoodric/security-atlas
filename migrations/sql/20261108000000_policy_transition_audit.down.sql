-- security-atlas — reverse 20261108000000_policy_transition_audit.sql.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'submitted', 'resubmitted' or 'approved' row exists
-- (the #2009 rule): the operator archives and removes them deliberately
-- first, or the narrowed CHECK could not apply.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log
    WHERE action IN ('submitted', 'resubmitted', 'approved');
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % submit/approve row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261108000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    DROP COLUMN IF EXISTS previous_state,
    DROP COLUMN IF EXISTS new_state;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved'));
