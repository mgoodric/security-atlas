-- security-atlas — #2200: policy submit and approve are audited.
--
-- Submit (draft -> under_review), re-submit (under_review -> under_review,
-- for a pre-#1807 submitter that names no person) and approve
-- (under_review -> approved) each append one policy_audit_log row in the
-- same transaction as the transition:
--
--   * action gains 'submitted', 'resubmitted' and 'approved'. A
--     self-approval keeps writing 'self_approved' (#2101) instead of
--     'approved', so one approval is one row;
--   * previous_state / new_state carry the policy status before and after
--     (NULL for the rows written before this migration and for actions
--     that are not status transitions).
--
-- Every existing action value is kept. policy_audit_log stays append-only
-- (its trigger is untouched) and keeps its RLS policies.
--
-- Reversible via 20261108000000_policy_transition_audit.down.sql.

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved')),
    ADD COLUMN previous_state TEXT NULL,
    ADD COLUMN new_state      TEXT NULL;
