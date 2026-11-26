-- security-atlas — #1806: policy review dates through the API.
--
-- policies.next_review_at (slice 094) could only be set by SQL or by the
-- #1747 import. #1806 lets an approver set it at publish and change it
-- afterwards through PATCH /v1/policies/{id}/review-date. That change does
-- not create a new policy version, so it is recorded in policy_audit_log:
--
--   * action gains 'review_date_updated';
--   * previous_next_review_at / new_next_review_at carry the before and
--     after values (NULL means "no review date").
--
-- policy_audit_log stays append-only (its trigger is untouched) and keeps
-- its RLS policies; this migration only widens the action CHECK and adds
-- two nullable columns.
--
-- Reversible via 20260903000000_policy_review_dates.down.sql.

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated')),
    ADD COLUMN previous_next_review_at TIMESTAMPTZ NULL,
    ADD COLUMN new_next_review_at      TIMESTAMPTZ NULL;
