-- #2106: linking a control to a risk and unlinking one are logged. Each
-- writes one risk_audit_log row in the link's transaction: control_linked
-- (before {control_id: null}, after {control_id}) and control_unlinked
-- (the reverse). The unified log's risk branch names the changed field
-- (control_id) and never its value.
--
-- The full list from 20261212000000_risk_lifecycle.sql plus the two new
-- values.

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
        'control_unlinked'
    ));
