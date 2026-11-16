-- security-atlas — #2446: draft edits are attributed, and every draft
-- editor counts as an author for the approval rule.
--
--   * policies.draft_editors holds the principal (credstore.PrincipalOf:
--     the bare users(id) uuid for a person) of whoever created the draft
--     and of everyone who edited it through PATCH /v1/policies/{id}. The
--     approval rule (#1807 / #2101) refuses each of them as approver
--     unless self-approval is on, and then flags the approval. Rows
--     written before this migration start empty, so they keep the
--     submitter-only check they were created under;
--   * policy_audit_log.action gains 'draft_edited', one row per edit;
--   * policy_audit_log.body_sha256 is the hex sha256 of body_md on the
--     'draft_edited' and 'submitted' / 'resubmitted' rows. The body itself
--     is never copied into the log;
--   * policy_audit_log.editors is the editor set on 'submitted' /
--     'resubmitted' rows.
--
-- Every existing action value is kept. policy_audit_log stays append-only
-- (its trigger is untouched) and keeps its RLS policies.
--
-- Reversible via 20261222000000_policy_draft_editors.down.sql.

ALTER TABLE policies
    ADD COLUMN draft_editors TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited')),
    ADD COLUMN body_sha256 TEXT NULL CHECK (body_sha256 IS NULL OR body_sha256 ~ '^[0-9a-f]{64}$'),
    ADD COLUMN editors     TEXT[] NULL;
