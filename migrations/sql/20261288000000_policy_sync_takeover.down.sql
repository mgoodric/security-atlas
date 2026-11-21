-- security-atlas -- reverse 20261288000000_policy_sync_takeover.sql.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any 'sync_takeover' row exists, or any row names a previous
-- attribution: the operator archives and removes them deliberately first.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log
    WHERE action = 'sync_takeover'
       OR previous_source_attribution IS NOT NULL;
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % sync_takeover row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261288000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    DROP COLUMN IF EXISTS previous_source_attribution;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped', 'synced', 'controls_updated'));
