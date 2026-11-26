-- security-atlas — #1781: optional LLM rerank of control import candidates
-- (bring-your-own controls, #1734).
--
-- When the organisation has opted in to a cloud LLM, the importer asks it to
-- reorder each staged row's deterministic candidates (#1780) and write a
-- short rationale for each. The LLM never adds or removes an anchor. If it is
-- unavailable or fails, the deterministic list stays as it was and the row
-- records that no rationale is available.
--
--   ai_generations.surface   gains 'control_import', so import usage is not
--                            counted as questionnaire usage.
--   control_import_candidates
--                            gains rationale plus the AI-assist boundary
--                            columns, guarded by the shared
--                            ai_assist_human_approver_guard (slice 498).
--                            source stays the candidate's origin
--                            (crosswalk | keyword); ai_assisted marks a
--                            candidate the LLM placed and explained.
--   control_import_rows.llm_rerank
--                            off | applied | unavailable.

ALTER TABLE ai_generations DROP CONSTRAINT IF EXISTS ai_generations_surface_chk;
ALTER TABLE ai_generations
    ADD CONSTRAINT ai_generations_surface_chk
    CHECK (surface IN (
        'questionnaire',
        'board_narrative',
        'gap_explanation',
        'checklist',
        'summary',
        'control_import'
    ));

ALTER TABLE control_import_candidates
    ADD COLUMN IF NOT EXISTS rationale TEXT NULL,
    ADD COLUMN IF NOT EXISTS ai_assisted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS human_approved BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS human_approver TEXT NULL;

DO $$
BEGIN
    ALTER TABLE control_import_candidates
        ADD CONSTRAINT control_import_candidates_ai_assist_invariant
        CHECK (ai_assist_human_approver_guard(ai_assisted, human_approved, human_approver));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE control_import_candidates
        ADD CONSTRAINT control_import_candidates_rationale_ai_chk
        CHECK (rationale IS NULL OR ai_assisted);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE control_import_rows
    ADD COLUMN IF NOT EXISTS llm_rerank TEXT NOT NULL DEFAULT 'off';

DO $$
BEGIN
    ALTER TABLE control_import_rows
        ADD CONSTRAINT control_import_rows_llm_rerank_chk
        CHECK (llm_rerank IN ('off', 'applied', 'unavailable'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
