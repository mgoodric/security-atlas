-- #2961: a child risk's History reads the child_linked / child_unlinked
-- rows on its parents' trails (ListRiskAuditLog), matched on the child id
-- in the row's state: after_state for a link, before_state for an unlink.
-- This partial index serves that match, so the read does not scan every
-- hierarchy row in the tenant. No row is written or changed.

CREATE INDEX IF NOT EXISTS risk_audit_log_child_link
    ON risk_audit_log (
        tenant_id,
        (COALESCE(after_state->>'child_risk_id', before_state->>'child_risk_id'))
    )
    WHERE action_type IN ('child_linked', 'child_unlinked');
