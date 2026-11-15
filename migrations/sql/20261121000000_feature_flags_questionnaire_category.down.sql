-- Reverses 20261121000000_feature_flags_questionnaire_category.sql:
-- restores the slice-059 feature_flags category CHECK.
--
-- A 'questionnaire' row is a tenant's stored switch. It is not deleted
-- here; remove it deliberately first, or the narrowed CHECK refuses to
-- apply.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM feature_flags WHERE category = 'questionnaire';
    IF n > 0 THEN
        RAISE EXCEPTION 'feature_flags holds % questionnaire row(s); refusing to delete tenant switches', n
            USING HINT = 'Remove those rows deliberately before reversing 20261121000000.';
    END IF;
END
$$;

ALTER TABLE feature_flags
    DROP CONSTRAINT IF EXISTS feature_flags_category_chk;

ALTER TABLE feature_flags
    ADD CONSTRAINT feature_flags_category_chk
    CHECK (category IN (
        'core', 'risk', 'vendor', 'policy', 'controls',
        'audit', 'evidence', 'board', 'integrations'
    ));
