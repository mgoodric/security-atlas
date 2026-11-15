-- #2170: Questionnaires becomes an optional module with its own switch,
-- `questionnaires.enabled` (internal/featureflag Seed + Modules). Its flag
-- rows carry category 'questionnaire', which the slice-059 CHECK did not
-- list. Widen the CHECK; no rows change.

ALTER TABLE feature_flags
    DROP CONSTRAINT IF EXISTS feature_flags_category_chk;

ALTER TABLE feature_flags
    ADD CONSTRAINT feature_flags_category_chk
    CHECK (category IN (
        'core', 'risk', 'vendor', 'policy', 'controls',
        'audit', 'evidence', 'board', 'integrations',
        'questionnaire'
    ));
