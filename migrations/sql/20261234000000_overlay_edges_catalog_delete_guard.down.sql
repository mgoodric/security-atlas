-- Reverses 20261234000000_overlay_edges_catalog_delete_guard.sql.
--
-- The audit log is append-only, so existing 'cascade_removed' rows cannot
-- be removed; the restored CHECK is NOT VALID so they survive while new
-- rows are held to the earlier vocabulary.

DROP TRIGGER IF EXISTS fw_to_scf_overlay_edges_guard_requirement_tenant_trg ON fw_to_scf_overlay_edges;
DROP FUNCTION IF EXISTS fw_to_scf_overlay_edges_guard_requirement_tenant();

DROP TRIGGER IF EXISTS fw_to_scf_overlay_edges_guard_requirement_delete_trg ON framework_requirements;
DROP TRIGGER IF EXISTS fw_to_scf_overlay_edges_guard_anchor_delete_trg ON scf_anchors;
DROP FUNCTION IF EXISTS fw_to_scf_overlay_edges_guard_catalog_delete();

ALTER TABLE fw_to_scf_overlay_edge_audit_log
    DROP CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check;
ALTER TABLE fw_to_scf_overlay_edge_audit_log
    ADD CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check
    CHECK (action IN ('create', 'retire', 'carry_over')) NOT VALID;
