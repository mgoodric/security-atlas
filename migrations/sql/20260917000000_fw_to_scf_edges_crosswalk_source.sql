-- #1971: record which crosswalk source wrote each catalog edge.
--
-- The crosswalk importer prunes the edges a crosswalk no longer lists, so an
-- SCF remap that moves a mapping to a new anchor does not leave the old edge
-- counting as coverage. Two sources can write into the same framework
-- version, though: the bundled YAML crosswalk and an SCF workbook import
-- (docs/operator/scf-crosswalk-import.md, "Combining columns"). Both land as
-- community_draft, so source_attribution cannot tell them apart. Without
-- this column each import would delete the other's edges.
--
-- Values are written by the importer: 'yaml:<file name>' for a YAML
-- crosswalk, 'scf-workbook:<columns>' or 'scf-olir:<sheet>' for a workbook.
-- NULL means the edge predates this column (or was written by a seed that
-- does not set it). A YAML import treats NULL as its own, so an upgraded
-- install converges on its next YAML import; a workbook import never prunes
-- NULL edges.
--
-- Additive and nullable: no backfill, no rewrite of existing rows.

ALTER TABLE fw_to_scf_edges
    ADD COLUMN IF NOT EXISTS crosswalk_source TEXT;

COMMENT ON COLUMN fw_to_scf_edges.crosswalk_source IS
    'Crosswalk source that wrote this edge (yaml:<file>, scf-workbook:<columns>, scf-olir:<sheet>); NULL = predates #1971. The importer prunes only its own source.';
