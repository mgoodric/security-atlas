-- Down migration for slice 483 — drop the crosswalk mapping-tier surface for a
-- clean up->down->up round-trip.
--
-- Reverses 20260612080000_crosswalk_mapping_tier.sql in dependency order:
-- the audit table (FK to fw_to_scf_edges) first, then the column + its index,
-- then the enum. The existing source_attribution column + its data are NEVER
-- touched (P0-483-7) — this down migration is scoped strictly to the additive
-- mapping_tier surface.

DROP TABLE IF EXISTS fw_to_scf_edge_tier_transitions;

DROP INDEX IF EXISTS idx_fw_to_scf_edges_mapping_tier;

-- The column-level UPDATE grant is dropped implicitly with the column.
ALTER TABLE fw_to_scf_edges DROP COLUMN IF EXISTS mapping_tier;

DROP TYPE IF EXISTS crosswalk_mapping_tier;
