-- Reverses 20260916000000_scf_anchor_immutability.sql (#1768).
DROP TRIGGER IF EXISTS trg_scf_anchor_immutability ON scf_anchors;
DROP FUNCTION IF EXISTS enforce_scf_anchor_immutability();
