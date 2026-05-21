-- Reverses slice 173 (MCP write tools + HITL approval).
--
-- DROP TABLE cascades the four RLS policies + both indexes + every CHECK
-- constraint, so this is a single statement. Re-applying the .sql migration
-- restores the pre-slice schema.

DROP TABLE IF EXISTS mcp_write_proposals;
