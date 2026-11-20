-- security-atlas — reverse the schema of
-- 20261272000000_policy_audience_generic_to_everyone.sql.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'audience_mapped' row exists, or any row carries an
-- audience (the #2009 rule): the operator archives and removes them
-- deliberately first. It does not restore the free-text audiences; the
-- previous_audience column of those rows is the record of what they were.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log
    WHERE action = 'audience_mapped' OR previous_audience IS NOT NULL OR new_audience IS NOT NULL;
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % audience-mapped row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261272000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    DROP COLUMN IF EXISTS previous_audience,
    DROP COLUMN IF EXISTS new_audience;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited'));
