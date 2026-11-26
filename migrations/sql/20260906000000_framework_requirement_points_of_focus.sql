-- framework_requirements.points_of_focus — SOC 2 points of focus rolled up
-- onto their parent criterion (#1731).
--
-- The SCF workbook's SOC 2 column mixes the TSC criteria with their points of
-- focus (`CC1.1-POF1`). An auditor tests against the criteria, so the importer
-- no longer makes a point of focus its own requirement: it folds it into the
-- parent criterion and records it here as metadata. Points of focus never
-- count toward requirement_count or coverage.
--
-- Shape: a JSON array of {"code": "CC1.1-POF1", "scf_anchors": ["GOV-01", ...]},
-- ordered by code. Empty for every requirement that has none, including all
-- bundled-YAML imports.
--
-- Written at INSERT time: the immutability trigger (20260612090000) rejects an
-- UPDATE of a frozen version's requirements, so the metadata must be complete
-- on first insert. The column default keeps existing rows and existing
-- INSERTs valid; re-running this migration is a no-op.
ALTER TABLE framework_requirements
    ADD COLUMN IF NOT EXISTS points_of_focus JSONB NOT NULL DEFAULT '[]'::jsonb;
