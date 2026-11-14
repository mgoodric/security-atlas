-- Reverse #1806: narrow policy_audit_log back to the import action.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'review_date_updated' row exists, rather than turning
-- off the append-only trigger and deleting them (#2009). The narrowed
-- CHECK would reject those rows, and the columns dropped below hold their
-- before and after dates. To reverse a database that has them, export or
-- archive the rows first and remove them deliberately as the owner.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log WHERE action = 'review_date_updated';
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % review_date_updated row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20260903000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported')),
    DROP COLUMN IF EXISTS new_next_review_at,
    DROP COLUMN IF EXISTS previous_next_review_at;
