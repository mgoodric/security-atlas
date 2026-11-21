-- security-atlas -- reverse 20261278000000_policy_sync.sql.
--
-- A synced policy is a published, audit-binding row, and its 'synced'
-- policy_audit_log row is append-only audit evidence. This reversal
-- refuses to run while either exists (the #2009 rule): the operator
-- archives and removes them deliberately first, or the narrowed CHECKs
-- could not apply.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policies WHERE source_attribution = 'synced';
    IF n > 0 THEN
        RAISE EXCEPTION 'policies holds % synced row(s); refusing to delete published policies', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261278000000.';
    END IF;
    SELECT count(*) INTO n FROM policy_audit_log WHERE action = 'synced';
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % synced row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261278000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped'));

DROP INDEX IF EXISTS idx_policies_synced_page;

ALTER TABLE policies
    DROP CONSTRAINT IF EXISTS policies_synced_historical_chk,
    DROP CONSTRAINT IF EXISTS policies_synced_fields_chk,
    DROP CONSTRAINT IF EXISTS policies_source_attribution_chk;

ALTER TABLE policies
    ADD CONSTRAINT policies_source_attribution_chk
        CHECK (source_attribution IN ('community_draft', 'tenant_authored', 'vendor_provided', 'imported'));

ALTER TABLE policies
    DROP COLUMN IF EXISTS synced_at,
    DROP COLUMN IF EXISTS synced_by,
    DROP COLUMN IF EXISTS synced_body_sha256,
    DROP COLUMN IF EXISTS sync_page_id,
    DROP COLUMN IF EXISTS sync_space,
    DROP COLUMN IF EXISTS sync_site;
