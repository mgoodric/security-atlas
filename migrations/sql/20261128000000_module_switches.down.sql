-- Reverses 20261128000000_module_switches.sql where it can without losing
-- a tenant's stored switch:
--
-- - `vendor.claims` rows move back to `oscal.export` (category
--   'integrations'), value unchanged.
-- - The narrowed category CHECK is restored.
--
-- Refuses rather than deletes: a stored `personnel.enabled` or
-- `audit.oscal_export` row is a tenant's switch that the old schema has no
-- place for, so the down migration stops until those rows are removed
-- deliberately.
--
-- The up migration's reset of four no-op module switches from off to on is
-- not undone: which rows were off is kept only in feature_flag_audit_log
-- (actor 'migration:20261128000000'), and those switches hid nothing before
-- the up migration.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM feature_flags
     WHERE flag_key IN ('personnel.enabled', 'audit.oscal_export')
        OR category = 'personnel';
    IF n > 0 THEN
        RAISE EXCEPTION 'feature_flags holds % personnel.enabled / audit.oscal_export row(s); refusing to delete tenant switches', n
            USING HINT = 'Remove those rows deliberately before reversing 20261128000000.';
    END IF;
END
$$;

UPDATE feature_flags
   SET flag_key = 'oscal.export',
       category = 'integrations'
 WHERE flag_key = 'vendor.claims';

ALTER TABLE feature_flags
    DROP CONSTRAINT IF EXISTS feature_flags_category_chk;

ALTER TABLE feature_flags
    ADD CONSTRAINT feature_flags_category_chk
    CHECK (category IN (
        'core', 'risk', 'vendor', 'policy', 'controls',
        'audit', 'evidence', 'board', 'integrations',
        'questionnaire'
    ));
