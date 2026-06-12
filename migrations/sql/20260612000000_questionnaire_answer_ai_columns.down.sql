-- Down migration for slice 441 — drop the AI-assist boundary columns +
-- constraints from questionnaire_answers for a clean up->down->up round-trip.
-- The shared ai_assist_human_approver_guard function is owned by slice 498 and
-- is NOT dropped here (other adopters depend on it).

ALTER TABLE questionnaire_answers
    DROP CONSTRAINT IF EXISTS questionnaire_answers_ai_provenance_nonempty;
ALTER TABLE questionnaire_answers
    DROP CONSTRAINT IF EXISTS questionnaire_answers_ai_assist_invariant;

ALTER TABLE questionnaire_answers
    DROP COLUMN IF EXISTS model_provider,
    DROP COLUMN IF EXISTS model_version,
    DROP COLUMN IF EXISTS model_name,
    DROP COLUMN IF EXISTS prompt_version,
    DROP COLUMN IF EXISTS human_approver,
    DROP COLUMN IF EXISTS human_approved,
    DROP COLUMN IF EXISTS ai_assisted;
