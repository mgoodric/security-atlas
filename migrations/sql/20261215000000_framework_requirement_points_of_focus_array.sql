-- framework_requirements.points_of_focus is always a JSON array (#2026).
--
-- #1731 added the column as JSONB NOT NULL DEFAULT '[]' and documented its
-- shape as an array of {"code", "scf_anchors"}, but nothing in the database
-- held it to an array: a scalar or an object would have been stored, and
-- every reader (the importer's equality check, the coverage and anchor
-- endpoints) assumes an array.
--
-- Added NOT VALID, then validated, so the ADD takes only a brief lock and the
-- full-table check runs under the weaker lock VALIDATE needs. No backfill:
-- the column default is '[]', and its only writer (the crosswalk importer's
-- marshalPointsOfFocus) writes '[]' or a json.Marshal of a non-empty Go
-- slice, which is always an array. A backfill could not run anyway, because
-- the immutability trigger (20260612090000) rejects an UPDATE of a frozen
-- version's requirements; a row that fails here fails the migration loudly.
ALTER TABLE framework_requirements
    DROP CONSTRAINT IF EXISTS framework_requirements_points_of_focus_array;
ALTER TABLE framework_requirements
    ADD CONSTRAINT framework_requirements_points_of_focus_array
    CHECK (jsonb_typeof(points_of_focus) = 'array') NOT VALID;
ALTER TABLE framework_requirements
    VALIDATE CONSTRAINT framework_requirements_points_of_focus_array;
