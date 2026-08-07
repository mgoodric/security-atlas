-- Down migration for 20260804000000_crosswalk_edge_content_edits.sql.
--
-- Restores slice 483's narrow-grant posture: atlas_app loses UPDATE on the
-- STRM content columns (the D-536b-2 widening) and the content-edit audit
-- table is dropped. Slice 483's own UPDATE (mapping_tier, updated_at) grant
-- and fw_to_scf_edge_tier_transitions are untouched — they belong to
-- migration 20260612080000.

REVOKE UPDATE (relationship_type, strength, rationale) ON fw_to_scf_edges FROM atlas_app;

DROP TABLE IF EXISTS fw_to_scf_edge_content_edits;
