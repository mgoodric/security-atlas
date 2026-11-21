-- security-atlas -- #2852: control links on synced policies (Confluence
-- policy sync, #2813).
--
-- A synced policy is read-only in security-atlas: its text, version and
-- review date come from its Confluence page. Its control links do not
-- (the page carries none), so PATCH /v1/policies/{id}/controls replaces a
-- published synced policy's linked_control_ids in place, without a new
-- version. Each change appends one policy_audit_log row:
--
--   * action gains 'controls_updated'.
--   * previous_linked_control_ids / new_linked_control_ids hold the
--     links before and after the change. NULL on every other action.
--
-- policy_audit_log stays append-only (its trigger is untouched) and keeps
-- its RLS policies (constitutional invariant #6); this migration adds no
-- table.
--
-- Reversible via 20261283000000_policy_controls_updated.down.sql.

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped', 'synced', 'controls_updated'));

ALTER TABLE policy_audit_log
    ADD COLUMN IF NOT EXISTS previous_linked_control_ids UUID[] NULL,
    ADD COLUMN IF NOT EXISTS new_linked_control_ids      UUID[] NULL;
