-- Reverse of 20260612110000_questionnaire_answer_runs.sql.

DROP TRIGGER IF EXISTS trg_questionnaire_answer_run_status_transition
    ON questionnaire_answer_runs;
DROP FUNCTION IF EXISTS questionnaire_answer_run_status_transition_guard();

DROP TABLE IF EXISTS questionnaire_answer_run_items;
DROP TABLE IF EXISTS questionnaire_answer_runs;
