-- security-atlas — MCP write tools for risk hierarchy links (#1748).
--
-- Two write tools join the MCP proposal surface: link_child_risks attaches
-- existing risks as children of an existing parent risk, and
-- unlink_child_risk detaches one. Both go through the same HITL approval as
-- the four slice-173 tools: a proposal sits at ai_proposed until a human
-- confirms it, and the applier runs internal/risk's link validation (level
-- order, no cycles, same tenant).
--
-- The tool-name CHECK lists every allowed tool, so a new tool needs this
-- migration (see the CHECK's own comment in 20260520030000).

ALTER TABLE mcp_write_proposals DROP CONSTRAINT mcp_wp_tool_name_check;
ALTER TABLE mcp_write_proposals ADD CONSTRAINT mcp_wp_tool_name_check
    CHECK (tool_name IN (
        'create_risk',
        'update_control_state',
        'push_evidence',
        'update_risk_treatment',
        'link_child_risks',
        'unlink_child_risk'
    ));
