-- security-atlas -- #1812: a risk's category change is audit-logged.
--
-- PATCH /v1/risks/{id} now takes `category` (the migrate step of #1745), so
-- a risk can be moved onto another of the tenant's categories, including
-- off one that has been archived. Each change writes one risk_audit_log row
-- in the same transaction, beside the existing placement rows.

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN ('placement_changed', 'category_changed'));
