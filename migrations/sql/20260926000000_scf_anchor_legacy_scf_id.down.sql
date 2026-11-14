-- Reverses 20260926000000_scf_anchor_legacy_scf_id.sql (#1769).
ALTER TABLE scf_anchors DROP COLUMN IF EXISTS legacy_scf_id;
