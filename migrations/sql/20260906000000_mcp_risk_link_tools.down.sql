-- Reverse #1748: drop the two risk-link MCP tools from the tool-name CHECK.
-- NOT VALID keeps any proposal already filed for them (proposals are the
-- AI-assist audit trail and are never deleted) while refusing new ones.
--
-- Caveat: a NOT VALID CHECK still applies to UPDATEs. After this rollback a
-- link_child_risks / unlink_child_risk proposal that is still ai_proposed
-- cannot be confirmed or rejected (both UPDATE the row) until the forward
-- migration is re-applied. Applied and rejected proposals are terminal and
-- unaffected.

ALTER TABLE mcp_write_proposals DROP CONSTRAINT mcp_wp_tool_name_check;
ALTER TABLE mcp_write_proposals ADD CONSTRAINT mcp_wp_tool_name_check
    CHECK (tool_name IN (
        'create_risk',
        'update_control_state',
        'push_evidence',
        'update_risk_treatment'
    )) NOT VALID;
