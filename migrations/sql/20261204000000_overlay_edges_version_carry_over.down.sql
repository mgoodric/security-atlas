-- Reverses 20261204000000_overlay_edges_version_carry_over.sql.
--
-- Copied edges stay: they are ordinary active overlay edges. The audit log
-- is append-only, so existing 'carry_over' rows cannot be removed; the
-- restored CHECK is NOT VALID so they survive while new rows are held to
-- the original vocabulary.

DROP FUNCTION IF EXISTS carry_over_overlay_edges(UUID, TEXT);

ALTER TABLE fw_to_scf_overlay_edge_audit_log
    DROP CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check;
ALTER TABLE fw_to_scf_overlay_edge_audit_log
    ADD CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check
    CHECK (action IN ('create', 'retire')) NOT VALID;
