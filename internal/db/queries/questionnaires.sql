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

-- name: UpsertAISuggestedAnswer :one
-- Slice 441 — persist an AI-suggested DRAFT answer for one question. The draft
-- is ai_assisted=TRUE, human_approved=FALSE, human_approver=NULL: a suggestion
-- the operator has not yet approved (P0-441-1, AC-6). The model-provenance
-- columns are populated from the generation that produced the draft
-- (snapshot-at-generation). On conflict (the question already has an answer)
-- the draft REPLACES the prior answer text BUT resets approval to FALSE/NULL —
-- a fresh suggestion is unapproved by construction, so an operator can never
-- inherit a prior approval onto new AI text (P0-441-1).
INSERT INTO questionnaire_answers
    (id, tenant_id, question_id, answer_value, narrative, citations, authored_by,
     ai_assisted, human_approved, human_approver,
     prompt_version, model_name, model_version, model_provider)
VALUES ($1, $2, $3, $4, $5, $6, $7,
        TRUE, FALSE, NULL,
        $8, $9, $10, $11)
ON CONFLICT (question_id) DO UPDATE
SET answer_value    = EXCLUDED.answer_value,
    narrative       = EXCLUDED.narrative,
    citations       = EXCLUDED.citations,
    authored_by     = EXCLUDED.authored_by,
    ai_assisted     = TRUE,
    human_approved  = FALSE,
    human_approver  = NULL,
    prompt_version  = EXCLUDED.prompt_version,
    model_name      = EXCLUDED.model_name,
    model_version   = EXCLUDED.model_version,
    model_provider  = EXCLUDED.model_provider,
    updated_at      = now()
RETURNING *;

-- name: ApproveQuestionnaireAnswer :one
-- Slice 441 — one-click human approval of an AI-suggested draft (AC-6/AC-7).
-- Sets human_approved=TRUE and records the human_approver; optionally accepts
-- the operator's edited final text. The DB CHECK
-- questionnaire_answers_ai_assist_invariant makes human_approved=TRUE with a
-- blank human_approver impossible (P0-441-8); this query NEVER passes an empty
-- approver (the service rejects that before the round-trip via
-- llm.EnforceApproval). Tenant-scoped by RLS + the explicit tenant_id guard.
UPDATE questionnaire_answers
SET narrative       = $3,
    answer_value    = $4,
    human_approved  = TRUE,
    human_approver  = $5,
    updated_at      = now()
WHERE tenant_id = $1
  AND id = $2
  AND ai_assisted = TRUE
RETURNING *;

-- name: GetQuestionnaireAnswerByID :one
-- Slice 441 — fetch one answer by id under the caller's tenant. Used by the
-- approval path to confirm the draft exists + is AI-assisted before approving,
-- and by tests. A cross-tenant id returns ErrNoRows (RLS).
SELECT * FROM questionnaire_answers
WHERE tenant_id = $1 AND id = $2;
