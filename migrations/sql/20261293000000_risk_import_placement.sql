-- security-atlas — #2845: level and org unit placement on staged risk
-- import rows (spec #2812, Q17).
--
-- risk_import_rows gains the placement approval creates the risk with:
--
--   level           the level the row's level cell names (team, org,
--                   company); NULL when it names none, so the create path
--                   takes the org unit's level, or team without one;
--   org_unit_value  the org unit cell as written, kept for review;
--   org_unit_id     the tenant org unit that cell matched by name, ignoring
--                   case. NULL when the row names no unit.
--
-- No foreign key on org_unit_id: a unit removed after upload leaves the row
-- in place, and approval refuses it through the create path's placement
-- rule (the row stays pending with the reason), as the import's other
-- staged references do.
--
-- Four new rejection reasons: invalid_level (a value that is no level),
-- unknown_org_unit (a name that matches none of the tenant's org units),
-- ambiguous_org_unit (a name two of them share) and level_mismatch (a level
-- that differs from the matched unit's).

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS level          risk_level NULL,
    ADD COLUMN IF NOT EXISTS org_unit_value TEXT       NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS org_unit_id    UUID       NULL;

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_org_unit_chk;
ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_org_unit_chk
        CHECK ((org_unit_id IS NULL) = (org_unit_value = ''));

ALTER TABLE risk_import_rejected_rows
    DROP CONSTRAINT IF EXISTS risk_import_rejected_rows_reason_chk;
ALTER TABLE risk_import_rejected_rows
    ADD CONSTRAINT risk_import_rejected_rows_reason_chk
        CHECK (reason IN ('missing_required_field', 'duplicate_external_ref',
                          'invalid_external_ref', 'unknown_category',
                          'invalid_review_due', 'invalid_score',
                          'invalid_treatment', 'invalid_acceptance',
                          'invalid_level', 'unknown_org_unit',
                          'ambiguous_org_unit', 'level_mismatch'));
