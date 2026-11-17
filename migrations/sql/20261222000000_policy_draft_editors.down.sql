-- security-atlas — reverse 20261222000000_policy_draft_editors.sql.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'draft_edited' row exists, or any row carries a body
-- hash or an editor set (the #2009 rule): the operator archives and
-- removes them deliberately first.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log
    WHERE action = 'draft_edited' OR body_sha256 IS NOT NULL OR editors IS NOT NULL;
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % draft-edit or hashed row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261222000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    DROP COLUMN IF EXISTS body_sha256,
    DROP COLUMN IF EXISTS editors;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved'));

ALTER TABLE policies
    DROP COLUMN IF EXISTS draft_editors;
