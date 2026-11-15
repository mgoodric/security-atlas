-- security-atlas -- #2104: a risk's record-field edits are audit-logged.
--
-- PATCH /v1/risks/{id} now changes the record fields (title, description,
-- methodology, inherent score, treatment owner, review due, instrument
-- reference). Each edit that changes something writes one risk_audit_log
-- row in the same transaction, holding the before and after value of each
-- changed field, beside the placement and category rows.
--
-- The full list from 20261030000000_risk_audit_category_changed.sql, plus
-- fields_changed.

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN ('placement_changed', 'category_changed', 'fields_changed'));
