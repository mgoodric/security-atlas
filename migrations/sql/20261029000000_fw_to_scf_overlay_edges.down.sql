-- Reverses 20261029000000_fw_to_scf_overlay_edges.sql.
DROP TABLE IF EXISTS fw_to_scf_overlay_edge_audit_log;
DROP FUNCTION IF EXISTS fw_to_scf_overlay_edge_audit_log_append_only();
DROP TABLE IF EXISTS fw_to_scf_overlay_edges;
DROP FUNCTION IF EXISTS fw_to_scf_overlay_edges_guard_update();
