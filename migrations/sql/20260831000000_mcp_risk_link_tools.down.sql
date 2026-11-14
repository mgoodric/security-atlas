-- Reverse #1748: drop the two risk-link MCP tools from the tool-name CHECK.
-- NOT VALID keeps any proposal already filed for them (proposals are the
-- AI-assist audit trail and are never deleted) while refusing new ones.

ALTER TABLE mcp_write_proposals DROP CONSTRAINT mcp_wp_tool_name_check;
ALTER TABLE mcp_write_proposals ADD CONSTRAINT mcp_wp_tool_name_check
    CHECK (tool_name IN (
        'create_risk',
        'update_control_state',
        'push_evidence',
        'update_risk_treatment'
    )) NOT VALID;
