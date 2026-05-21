-- Reverse of 20260521000000_questionnaire_tracer.sql. Drops the four
-- new tables in topological order (children first). IF EXISTS keeps
-- the down idempotent.

DROP TABLE IF EXISTS answer_library;
DROP TABLE IF EXISTS questionnaire_answers;
DROP TABLE IF EXISTS questionnaire_questions;
DROP TABLE IF EXISTS questionnaires;
