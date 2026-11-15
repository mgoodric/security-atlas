-- #1971: record which crosswalk source wrote each catalog edge, and retire
-- edges a crosswalk drops instead of deleting them.
--
-- The crosswalk importer retires the edges a crosswalk no longer lists, so
-- an SCF remap that moves a mapping to a new anchor (or a new SCF release
-- that mints new anchor rows) does not leave the old edge counting as
-- coverage.
--
-- crosswalk_source: two sources can write into the same framework version,
-- the bundled YAML crosswalk and an SCF workbook import
-- (docs/operator/scf-crosswalk-import.md, "Combining columns"). Both land as
-- community_draft, so source_attribution cannot tell them apart. Values are
-- written by the importer: 'yaml:<file name>' for a YAML crosswalk,
-- 'scf-workbook:<columns>' or 'scf-olir:<sheet>' for a workbook. NULL means
-- the edge predates this column (or was written by a seed that does not set
-- it). A YAML import treats NULL as its own; a workbook import never retires
-- a NULL edge.
--
-- retired_at: a retired edge is kept, with its tier-transition history
-- (fw_to_scf_edge_tier_transitions is append-only), and is invisible to
-- coverage, traversal, export and the tier state machine. The importer
-- writes a transition row when it retires an edge and when a crosswalk
-- lists it again, which clears retired_at.
--
-- Additive and nullable: no backfill, no rewrite of existing rows.

ALTER TABLE fw_to_scf_edges
    ADD COLUMN IF NOT EXISTS crosswalk_source TEXT,
    ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ;

COMMENT ON COLUMN fw_to_scf_edges.crosswalk_source IS
    'Crosswalk source that wrote this edge (yaml:<file>, scf-workbook:<columns>, scf-olir:<sheet>); NULL = predates #1971. The importer retires only its own source''s edges.';
COMMENT ON COLUMN fw_to_scf_edges.retired_at IS
    'Set when the crosswalk that wrote this edge stopped listing it (#1971); cleared when it lists it again. Retired edges are excluded from effective_fw_to_scf_edges.';

-- The single coverage/traversal read path (#1796) excludes retired edges.
-- Same column list as migration 20260821000000, so dependents are unchanged.
CREATE OR REPLACE VIEW effective_fw_to_scf_edges
WITH (security_invoker = true) AS
SELECT
    e.id,
    e.framework_requirement_id,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.rationale,
    e.created_at,
    e.updated_at,
    e.mapping_tier
FROM fw_to_scf_edges e
WHERE e.retired_at IS NULL;

GRANT SELECT ON effective_fw_to_scf_edges TO atlas_app;
