-- Down migration for slice 536b — drop the crosswalk content-edit surface for a
-- clean up->down->up round-trip.
--
-- Reverses 20260612110000_crosswalk_content_edit.sql in dependency order: the
-- audit table (FK to fw_to_scf_edges) first, then re-narrow the column grant
-- back to exactly what slice 483 D1 granted. REVOKE names only the columns this
-- migration added, so the 483 `UPDATE (mapping_tier, updated_at)` privilege
-- survives the rollback — dropping the whole UPDATE privilege here would break
-- the tier-transition path, which this migration did not introduce.
--
-- The edge rows themselves are never touched: an edit already applied to
-- relationship_type / strength / rationale is real catalog data, not part of
-- this migration's additive surface. Rolling the migration back removes the
-- ability to make further edits and the trail of the ones made; it does not
-- rewrite the catalog.

DROP TABLE IF EXISTS fw_to_scf_edge_content_edits;

REVOKE UPDATE (relationship_type, strength, rationale) ON fw_to_scf_edges FROM atlas_app;
