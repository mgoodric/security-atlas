-- Slice 155: questionnaire tracer-bullet queries.
--
-- CRUD against the four new tables (questionnaires, questionnaire_questions,
-- questionnaire_answers, answer_library). Every query is tenant-bound via
-- the leading $1 parameter (defense-in-depth behind RLS). Decision D6
-- carves the AnswerLibrary "give me priors for this anchor" query out to
-- the pgx layer instead of sqlc — the conditional LIMIT shape doesn't
-- generate cleanly through sqlc v1.31.1 on PostgreSQL with pgx/v5 — so
-- it's NOT in this file.

-- name: InsertQuestionnaire :one
-- Create one new draft questionnaire. status defaults to 'draft' and is
-- not set explicitly here (the table default applies).
INSERT INTO questionnaires (id, tenant_id, name, source_label, source_filename)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetQuestionnaireByID :one
-- Fetch one questionnaire by id. RLS scopes the lookup to the caller's
-- tenant; a cross-tenant id returns ErrNoRows.
SELECT * FROM questionnaires
WHERE tenant_id = $1 AND id = $2;

-- name: ListQuestionnaires :many
-- Enumerate every questionnaire for the tenant, most recently updated
-- first. Powers the questionnaires landing page.
SELECT * FROM questionnaires
WHERE tenant_id = $1
ORDER BY updated_at DESC, id ASC;

-- name: InsertQuestionnaireQuestion :one
-- Append one question (from Excel import OR manual authoring). The
-- (questionnaire_id, code) UNIQUE constraint protects against duplicate
-- imports of the same row.
INSERT INTO questionnaire_questions
    (id, tenant_id, questionnaire_id, code, text, domain, answer_type, scf_anchor_id, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListQuestionsForQuestionnaire :many
-- Enumerate every question for a questionnaire in stable display order.
SELECT * FROM questionnaire_questions
WHERE tenant_id = $1 AND questionnaire_id = $2
ORDER BY sort_order ASC, id ASC;

-- name: UpdateQuestionAnchor :one
-- Resolve a `needs_mapping` question by assigning it an SCF anchor.
UPDATE questionnaire_questions
SET scf_anchor_id = $3,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
RETURNING *;

-- name: UpsertQuestionnaireAnswer :one
-- Insert-or-update the single answer for a question. The unique
-- constraint on question_id makes this an upsert via ON CONFLICT.
INSERT INTO questionnaire_answers
    (id, tenant_id, question_id, answer_value, narrative, citations, authored_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (question_id) DO UPDATE
SET answer_value = EXCLUDED.answer_value,
    narrative    = EXCLUDED.narrative,
    citations    = EXCLUDED.citations,
    authored_by  = EXCLUDED.authored_by,
    updated_at   = now()
RETURNING *;

-- name: ListAnswersForQuestionnaire :many
-- All answers for a questionnaire, joined to the questions table so
-- callers can render the questionnaire end-to-end in a single read.
SELECT a.*
FROM questionnaire_answers a
JOIN questionnaire_questions q ON q.id = a.question_id
WHERE a.tenant_id = $1
  AND q.questionnaire_id = $2;

-- name: InsertAnswerLibraryEntry :one
-- Save an answer to the canonical library, keyed on SCF anchor.
INSERT INTO answer_library
    (id, tenant_id, scf_anchor_id, canonical_text, source_label, source_answer_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;
