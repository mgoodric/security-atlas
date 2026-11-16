-- #1769: SCF anchors carry their code in the previous SCF release.
--
-- The SCF workbook's `Legacy SCF #` column gives, for each control, the
-- code it had in the release before. The catalog JSON accepted by
-- `atlas-cli catalog import-scf` gains an optional per-anchor
-- `legacy_scf_id`; anchors store it here and anchor reads return it.
-- NULL means the catalog did not say (a new control, or a catalog that
-- predates the field).
--
-- Not a content column: trg_scf_anchor_immutability (migration
-- 20260916000000) leaves it out of its guard, so a release imported
-- before this column existed can have it backfilled by re-importing the
-- same release label with the field filled in.

ALTER TABLE scf_anchors ADD COLUMN legacy_scf_id TEXT;
