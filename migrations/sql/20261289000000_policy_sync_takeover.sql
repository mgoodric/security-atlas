-- security-atlas -- #2854: sync takes over imported policies (Confluence policy sync, #2813).
--
-- An adopter who ran the one-time policy import (#1747) before turning on
-- sync has a published, imported policy for each page. On a page's first
-- sync, the platform takes that policy over instead of starting a second
-- history beside it:
--
--   * same version: the imported row becomes the synced row in place;
--   * different version: the synced version is inserted as its next
--     version, chained to it and superseding it.
--
-- Each takeover is recorded in the policy audit log:
--
--   * policy_audit_log.action gains 'sync_takeover'. The row is written on
--     the taken-over policy; its actor is the policy-sync key's id.
--   * policy_audit_log.previous_source_attribution names the attribution
--     the policy had before ('imported'). NULL on every other row.
--
-- An in-place takeover clears imported_by and imported_at, which
-- policies_imported_fields_chk allows only on imported rows. The
-- 'imported' audit row the import wrote keeps who imported it and when.
--
-- Constitutional invariant #6: policy_audit_log keeps its RLS policies;
-- this migration adds no table.
--
-- Reversible via 20261289000000_policy_sync_takeover.down.sql.

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped', 'synced', 'controls_updated', 'sync_takeover'));

ALTER TABLE policy_audit_log
    ADD COLUMN IF NOT EXISTS previous_source_attribution TEXT NULL
        CHECK (previous_source_attribution IS NULL OR length(previous_source_attribution) BETWEEN 1 AND 64);
