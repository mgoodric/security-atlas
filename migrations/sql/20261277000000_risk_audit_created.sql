-- #2835: a risk's origin and its theme changes are logged. Every create
-- path (POST /v1/risks, a manual aggregation parent, an applied MCP
-- create_risk proposal, a rule-generated meta-risk) writes one `created`
-- row in the create's transaction: before {} and after the initial title,
-- category_id, treatment, owner_user_id and treatment_owner. Assigning or
-- removing a theme writes `themes_changed` (before/after {themes}).
--
-- The full list from 20261220000000_risk_control_link_audit.sql plus the
-- two new values.

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
        'acceptance_revoked',
        'closed',
        'reopened',
        'deleted',
        'control_linked',
        'control_unlinked',
        'created',
        'themes_changed'
    ));
