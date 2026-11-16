-- Reverse #1987: narrow policy_audit_log back to the import and review-date
-- actions.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'draft_discarded' row exists, rather than turning off
-- the append-only trigger and deleting them (#2009). The narrowed CHECK
-- would reject those rows, and the columns dropped below hold the
-- discarded title and version. To reverse a database that has them,
-- export or archive the rows first and remove them deliberately as the
-- owner.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log WHERE action = 'draft_discarded';
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % draft_discarded row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20260921000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated')),
    DROP COLUMN IF EXISTS policy_version,
    DROP COLUMN IF EXISTS policy_title;
