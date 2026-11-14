-- security-atlas — #1987: discard a policy draft through the API.
--
-- A draft created with predecessor_id holds the predecessor's single
-- successor slot (policies_predecessor_unique_when_set), so an abandoned
-- next-version draft blocked every further version with 409. #1987 adds
-- DELETE /v1/policies/{id}, which deletes a policy row only while it is a
-- draft, and records the discard in policy_audit_log:
--
--   * action gains 'draft_discarded';
--   * policy_title / policy_version carry what was discarded, because the
--     policies row the audit row names no longer exists afterwards
--     (policy_audit_log.policy_id has no FK, by design since #1747).
--
-- policy_audit_log stays append-only (its trigger is untouched) and keeps
-- its RLS policies; this migration only widens the action CHECK and adds
-- two nullable columns. atlas_app already holds DELETE on policies under
-- the tenant_delete RLS policy (slice 022); the draft-only rule is in the
-- DELETE statement itself.
--
-- Reversible via 20260921000000_policy_draft_discard.down.sql.

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded')),
    ADD COLUMN policy_title   TEXT NULL,
    ADD COLUMN policy_version TEXT NULL;
