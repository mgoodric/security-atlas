-- #2958: risk hierarchy links are logged. Attaching a child under a parent
-- (POST /v1/risks/{id}/children, the MCP link_child_risks applier, a risk
-- import's parent link) writes one `child_linked` row per new link on the
-- parent, in the link's transaction: before {child_risk_id: null}, after
-- {child_risk_id}. Detaching writes `child_unlinked` (before {child_risk_id},
-- after {child_risk_id: null}). A re-link of an existing child writes none.
--
-- The full list from 20261277000000_risk_audit_created.sql plus the two
-- new values.

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
        'themes_changed',
        'child_linked',
        'child_unlinked'
    ));
