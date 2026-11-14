-- Reverse #1971: drop the per-edge crosswalk source. The edges themselves
-- are untouched; only the record of which source wrote them is lost.

ALTER TABLE fw_to_scf_edges DROP COLUMN IF EXISTS crosswalk_source;
