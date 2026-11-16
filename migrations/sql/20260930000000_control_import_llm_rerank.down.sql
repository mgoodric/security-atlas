ALTER TABLE control_import_rows
    DROP CONSTRAINT IF EXISTS control_import_rows_llm_rerank_chk,
    DROP COLUMN IF EXISTS llm_rerank;
ALTER TABLE control_import_candidates
    DROP CONSTRAINT IF EXISTS control_import_candidates_rationale_ai_chk,
    DROP CONSTRAINT IF EXISTS control_import_candidates_ai_assist_invariant,
    DROP COLUMN IF EXISTS human_approver,
    DROP COLUMN IF EXISTS human_approved,
    DROP COLUMN IF EXISTS ai_assisted,
    DROP COLUMN IF EXISTS rationale;
-- ai_generations is append-only, so control_import rows written while this
-- migration was applied stay; NOT VALID keeps the restored CHECK from
-- failing on them while still rejecting new ones.
ALTER TABLE ai_generations DROP CONSTRAINT IF EXISTS ai_generations_surface_chk;
ALTER TABLE ai_generations
    ADD CONSTRAINT ai_generations_surface_chk
    CHECK (surface IN (
        'questionnaire',
        'board_narrative',
        'gap_explanation',
        'checklist',
        'summary'
    )) NOT VALID;
