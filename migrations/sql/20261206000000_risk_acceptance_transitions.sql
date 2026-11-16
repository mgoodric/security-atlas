-- security-atlas -- #2105: risk treatment and acceptance transitions.
--
-- Treatment and acceptance change only through transitions: change
-- treatment, propose acceptance, approve, reject and revoke. An approval
-- follows the shared approval rule (#2101, ADR-0024): approver differs
-- from proposer unless the organisation allows self-approval, which is then
-- flagged.
--
-- 1. risks gains the acceptance record a proposal and its approval need:
--    the justification, the proposed expiry, who proposed it and when, when
--    it was approved, and whether the approval was a self-approval
--    (self_approved, named as on the other approval tables so the shared
--    trigger applies). They are cleared when an acceptance is rejected or
--    revoked; the audit trail keeps them.
--
-- 2. risk_audit_log accepts one action per transition. The full list from
--    20261202000000_risk_audit_fields_changed.sql, plus the five new ones.
--
-- Existing proposed acceptances carry no proposer (acceptance_proposed_by
-- = ''); the approval rule has nothing to compare and admits any person.

ALTER TABLE risks ADD COLUMN acceptance_justification TEXT NOT NULL DEFAULT '';
ALTER TABLE risks ADD COLUMN acceptance_proposed_until DATE NULL;
ALTER TABLE risks ADD COLUMN acceptance_proposed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE risks ADD COLUMN acceptance_proposed_at TIMESTAMPTZ NULL;
ALTER TABLE risks ADD COLUMN acceptance_approved_at TIMESTAMPTZ NULL;
ALTER TABLE risks ADD COLUMN self_approved BOOLEAN NOT NULL DEFAULT false;

-- A self-approval is an approved acceptance with someone to name.
ALTER TABLE risks ADD CONSTRAINT risks_self_approved_is_approved
    CHECK (
        NOT self_approved
        OR (acceptance_status = 'approved' AND length(accepter) > 0)
    );

-- The setting, enforced in the database as on policies, framework scopes
-- and exceptions (20261031000000_self_approval.sql): a risk becomes
-- self_approved only while its organisation allows self-approval.
DROP TRIGGER IF EXISTS risks_self_approval_requires_setting_trg ON risks;
CREATE TRIGGER risks_self_approval_requires_setting_trg
    BEFORE INSERT OR UPDATE ON risks
    FOR EACH ROW
    EXECUTE FUNCTION self_approval_requires_setting();

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN (
        'placement_changed',
        'category_changed',
        'fields_changed',
        'treatment_changed',
        'acceptance_proposed',
        'acceptance_approved',
        'acceptance_rejected',
        'acceptance_revoked'
    ));
