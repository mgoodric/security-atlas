-- migrations/sql/20260612000000_questionnaire_answer_ai_columns.sql
--
-- Slice 441 — Questionnaire AI-answer suggestion v0 (cited drafts, one-click
-- approve). The FIRST AI-WRITE surface in security-atlas: an AI-suggested
-- questionnaire answer an operator approves and persists.
--
-- ----------------------------------------------------------------------------
-- WHY.
--
-- CLAUDE.md's AI-assist boundary names this exact gap:
--
--   "questionnaire_answers does NOT yet carry these columns on main — it gains
--    them (adopting the same shared guard) when this answer-suggestion surface
--    lands."
--
-- This migration lands them. It extends the slice-155 `questionnaire_answers`
-- table with the AI-assist boundary column set and ADOPTS the slice-498 shared
-- `ai_assist_human_approver_guard(...)` CHECK so the schema-level invariant
--
--     ai_assisted=TRUE => (human_approved=TRUE => human_approver present)
--
-- is enforced at the DB layer — identically to `mcp_write_proposals` (slice
-- 173) and any future approvable AI-assist record. The predicate is NOT
-- re-authored here (P0-498-4 discipline): we call the shared function.
--
-- ----------------------------------------------------------------------------
-- DESIGN CALLS (JUDGMENT slice — full rationale in
-- docs/audit-log/441-questionnaire-ai-v0-decisions.md):
--
--   * Columns ALTER an existing table. questionnaire_answers already exists
--     (slice 155); every new column is added NULLABLE or with a DEFAULT so the
--     ALTER is non-destructive and existing manually-authored answer rows stay
--     valid (they are ai_assisted=FALSE, which the guard permits in any
--     approval state). The slice-002 integration_test.go fixture inserts
--     answers via the questionnaire Store, not raw column lists, so no fixture
--     patch is required.
--
--   * The boundary column set mirrors the board-narrative schema contract
--     (CLAUDE.md §"Schema-level extensions"): prompt_version / model_name /
--     model_version / model_provider, PLUS the approval triple
--     ai_assisted / human_approved / human_approver. A manual answer leaves
--     the four model-provenance columns empty (their DEFAULT '') and
--     ai_assisted=FALSE; an AI-suggested draft populates all four at
--     suggestion time (snapshot-at-generation — a later config change never
--     rewrites history, same discipline as ai_generations).
--
--   * Provenance completeness is enforced ONLY when ai_assisted=TRUE. A manual
--     answer has no model, so requiring non-empty provenance unconditionally
--     would break every slice-155 row. The CHECK is therefore conditional:
--     ai_assisted=TRUE requires the four provenance columns non-empty;
--     ai_assisted=FALSE permits them empty.
--
--   * The append-only forensic record (prompt + candidate ids + raw draft +
--     final approved text) lives in the slice-498 `ai_generations` ledger
--     (surface='questionnaire', surface_subject=<answer id>), written by the
--     suggestion service. This table carries only the CURRENT answer state +
--     its approval columns; the immutable generation history is the ledger's
--     job. That keeps this table a single-row-per-question current-state table
--     (slice-155 invariant) and the audit trail append-only (R-mitigation).
--
-- ----------------------------------------------------------------------------
-- Constitutional invariants honored:
--
--   AI-assist boundary (hard): the shared CHECK is the schema-level
--     ai_assisted <-> human_approver enforcement; human_approved=TRUE without
--     human_approver is impossible at the DB layer (P0-441-8, AC-7). The
--     product never auto-approves — approval is a separate operator UPDATE
--     recording the approver.
--   #6  Tenant isolation: no RLS change — questionnaire_answers keeps its
--     slice-155 four-policy FORCE RLS. The new columns inherit it.
--   #9  Manual evidence is first-class: a manual answer (ai_assisted=FALSE)
--     renders the same surface; the guard never constrains it.
--
-- Idempotency / reversibility (AC-20): paired
-- 20260612000000_questionnaire_answer_ai_columns.down.sql drops the constraints
-- + columns for a clean up->down->up round-trip. No TYPE created.
-- ----------------------------------------------------------------------------

ALTER TABLE questionnaire_answers
    -- True when this answer originated from the AI suggestion surface. A
    -- manually-authored slice-155 answer stays FALSE (the column DEFAULT), so
    -- the guard never constrains it.
    ADD COLUMN ai_assisted    BOOLEAN NOT NULL DEFAULT FALSE,

    -- True once an operator has approved the AI-suggested draft (one-click
    -- approval per answer). A manual answer is FALSE until/unless promoted.
    ADD COLUMN human_approved  BOOLEAN NOT NULL DEFAULT FALSE,

    -- The operator id/credential that approved the answer. NULL until
    -- approved. The shared guard forbids ai_assisted=TRUE + human_approved=TRUE
    -- with this NULL/blank (P0-441-8).
    ADD COLUMN human_approver  TEXT NULL,

    -- Snapshot-at-generation model provenance (slice-182 schema contract,
    -- mirrors ai_generations). Empty for a manual answer; populated for an
    -- AI-suggested draft at suggestion time.
    ADD COLUMN prompt_version  TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_name      TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_version   TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_provider  TEXT NOT NULL DEFAULT '';

-- The AI-assist boundary CHECK, ADOPTED from the slice-498 reusable function
-- (NOT re-authored). Fails ONLY for the forbidden shape:
-- ai_assisted=TRUE AND human_approved=TRUE AND a missing-or-empty
-- human_approver. This is the schema-level mirror of internal/llm's
-- EnforceApproval (belt-and-suspenders: Go rejects early, the DB is
-- authoritative).
ALTER TABLE questionnaire_answers
    ADD CONSTRAINT questionnaire_answers_ai_assist_invariant
        CHECK (ai_assist_human_approver_guard(
            ai_assisted, human_approved, human_approver));

-- Provenance completeness, conditional on ai_assisted. An AI-suggested answer
-- must carry a reconstructable model (no row whose model is unknown —
-- R-mitigation); a manual answer is exempt because it has no model.
ALTER TABLE questionnaire_answers
    ADD CONSTRAINT questionnaire_answers_ai_provenance_nonempty
        CHECK (
            NOT ai_assisted
            OR (
                length(prompt_version) > 0
                AND length(model_name) > 0
                AND length(model_version) > 0
                AND length(model_provider) > 0
            )
        );
