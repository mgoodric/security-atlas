-- migrations/sql/20260521000000_questionnaire_tracer.sql
--
-- Slice 155 — Questionnaire feature, tracer-bullet scope.
--
-- Locked-in scope (maintainer, 2026-05-20):
--   - Excel-only import (CSV/JSON/Word DEFERRED as spillover slices)
--   - Manual answer authoring
--   - AnswerLibrary skeleton, KEYED ON SCF ANCHOR IDS (the load-bearing
--     design call — an answer is "what we say about IAC-06", not "what
--     we said when asked 'do you encrypt at rest'")
--   - PDF export
--
-- Schema shape:
--   - questionnaires          — the response instance (one per inbound
--                               questionnaire). Maps loosely onto canvas
--                               §4.6.2 `QuestionnaireResponse`.
--   - questionnaire_questions — one row per question in the inbound
--                               file, mapped to an SCF anchor when
--                               inferrable.
--   - questionnaire_answers   — one row per answered question.
--   - answer_library          — canonical answers keyed on SCF anchor.
--                               Future answers to questions mapping to
--                               the same anchor surface as suggestions
--                               (deterministic match, NOT AI inference).
--
-- All four tables are tenant-scoped under the four-policy RLS pattern
-- established by slices 014/017/018/036 (board_packs precedent). FORCE
-- ROW LEVEL SECURITY denies access on a missing session tenant — the
-- platform's tenancy invariant (canvas §5.4).
--
-- FK posture:
--   - questionnaires.tenant_id           → not a FK target (tenants live
--                                          outside this schema)
--   - questionnaire_questions.questionnaire_id → questionnaires(id) CASCADE
--   - questionnaire_questions.scf_anchor_id    → scf_anchors(id) RESTRICT, NULLABLE
--                                                (NULL = "needs manual mapping" — D5)
--   - questionnaire_answers.question_id        → questionnaire_questions(id) CASCADE
--   - answer_library.scf_anchor_id             → scf_anchors(id) RESTRICT, NOT NULL
--
-- NOT-NULL discipline: all new tables are NEW (zero ALTER on existing),
-- so no slice-002 integration_test.go helper-fixture patches required.

-- ===== questionnaires =====
--
-- A questionnaire response instance — "our answers to CAIQ for customer
-- X, on date Y". The lifecycle is draft → completed; once completed,
-- the answers are immutable (defense via tenant_update RLS predicate
-- below, mirroring the slice 032 board_packs pattern).

CREATE TABLE questionnaires (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    -- Human-readable name shown in lists. Required.
    name            TEXT NOT NULL,
    -- Source format / origin. Free-form so the operator can label
    -- "CAIQ v4.1", "SIG Lite 2026", "Acme custom". Not constrained;
    -- the canonical-template registry is a v2 follow-on.
    source_label    TEXT NOT NULL DEFAULT '',
    -- Filename of the imported xlsx (or empty for manually authored).
    source_filename TEXT NOT NULL DEFAULT '',
    -- Lifecycle.
    status          TEXT NOT NULL DEFAULT 'draft',
    -- Free-form notes — internal-only.
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT questionnaires_status_chk
        CHECK (status IN ('draft', 'completed')),
    CONSTRAINT questionnaires_name_nonempty
        CHECK (length(name) > 0)
);

CREATE INDEX idx_questionnaires_tenant_updated
    ON questionnaires (tenant_id, updated_at DESC);

ALTER TABLE questionnaires ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaires FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON questionnaires
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON questionnaires
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON questionnaires
    FOR UPDATE
    USING (current_tenant_matches(tenant_id) AND status = 'draft')
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON questionnaires
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON questionnaires TO atlas_app;

-- ===== questionnaire_questions =====
--
-- One row per question in the inbound file. scf_anchor_id is NULLABLE:
-- a NULL value means "the Excel parser could not infer an SCF anchor"
-- and the row is in `needs_mapping` state until the operator resolves
-- it via PATCH (decision D5). Upload is NEVER rejected for unmappable
-- questions.
--
-- The `code` column is the questionnaire's own per-question identifier
-- (e.g., "IAM-02", "G.1.1", "AAAI-04"). It's tenant-scoped and unique
-- within a questionnaire (a single CAIQ can't have two IAM-02 rows).

CREATE TABLE questionnaire_questions (
    id               UUID PRIMARY KEY,
    tenant_id        UUID NOT NULL,
    questionnaire_id UUID NOT NULL REFERENCES questionnaires (id) ON DELETE CASCADE,
    -- Per-questionnaire question identifier (the row's own code). For
    -- imported rows this is the source spreadsheet's question-ID cell;
    -- for manually authored questions it's operator-supplied.
    code             TEXT NOT NULL,
    -- The question text itself.
    text             TEXT NOT NULL,
    -- Domain / category label from the source file (e.g., "IAM", "DSI").
    -- Free-form; we don't constrain it to a canonical taxonomy.
    domain           TEXT NOT NULL DEFAULT '',
    -- Answer-shape hint from the source file. Free-form because every
    -- vendor questionnaire labels this differently ("yes/no/na",
    -- "Yes/No/N.A.", "scaled 1-5", "Free text").
    answer_type      TEXT NOT NULL DEFAULT '',
    -- Mapping to the canonical SCF anchor. NULL means "needs mapping".
    scf_anchor_id    TEXT NULL REFERENCES scf_anchors (id) ON DELETE RESTRICT,
    -- Display-ordering within the questionnaire. Stable on re-render.
    sort_order       INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT questionnaire_questions_code_nonempty
        CHECK (length(code) > 0),
    CONSTRAINT questionnaire_questions_text_nonempty
        CHECK (length(text) > 0),
    CONSTRAINT questionnaire_questions_unique_code_per_qn
        UNIQUE (questionnaire_id, code)
);

CREATE INDEX idx_qn_questions_tenant_qn_sort
    ON questionnaire_questions (tenant_id, questionnaire_id, sort_order);
CREATE INDEX idx_qn_questions_tenant_anchor
    ON questionnaire_questions (tenant_id, scf_anchor_id)
    WHERE scf_anchor_id IS NOT NULL;

ALTER TABLE questionnaire_questions ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_questions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON questionnaire_questions
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON questionnaire_questions
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON questionnaire_questions
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON questionnaire_questions
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON questionnaire_questions TO atlas_app;

-- ===== questionnaire_answers =====
--
-- One row per answered question. Separate from questionnaire_questions
-- so the question row stays stable across answer revisions and so an
-- import's questions can persist even when nothing is answered yet.
--
-- A question has AT MOST one answer at any time (UNIQUE on question_id).
-- Operator updates the answer text/value in place; we keep the latest
-- value rather than versioning per-revision (versioning would land as a
-- v2 follow-on if the operator workflow demands it).

CREATE TABLE questionnaire_answers (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    question_id     UUID NOT NULL UNIQUE REFERENCES questionnaire_questions (id) ON DELETE CASCADE,
    -- Discrete answer value for yes/no/na-style questions. Free-form
    -- because the source-file answer_type isn't constrained.
    answer_value    TEXT NOT NULL DEFAULT '',
    -- Narrative answer text — the operator's prose.
    narrative       TEXT NOT NULL DEFAULT '',
    -- Citations (evidence / policy / control IDs, JSONB so the shape
    -- can evolve). Empty-array default keeps queries simple.
    citations       JSONB NOT NULL DEFAULT '[]'::JSONB,
    -- Who authored the answer (display name; not an FK to users to
    -- keep this slice self-contained).
    authored_by     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_qn_answers_tenant_question
    ON questionnaire_answers (tenant_id, question_id);

ALTER TABLE questionnaire_answers ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_answers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON questionnaire_answers
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON questionnaire_answers
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON questionnaire_answers
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON questionnaire_answers
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON questionnaire_answers TO atlas_app;

-- ===== answer_library =====
--
-- Canonical, reusable answers KEYED ON SCF anchor. This is the
-- load-bearing design call (see slice 155 narrative): an entry says
-- "for SCF:IAC-06, here is the answer we last gave". Multiple entries
-- per (tenant, anchor) are allowed — surfaced as a list of priors,
-- most-recent-first (D2).
--
-- NOT keyed on (tenant, anchor) uniquely — multiple priors per anchor
-- are the whole point of "see prior answers". The suggestion lookup
-- query selects by (tenant_id, scf_anchor_id) ORDER BY updated_at DESC
-- LIMIT N. The B-tree index below makes that lookup index-only.

CREATE TABLE answer_library (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    scf_anchor_id   TEXT NOT NULL REFERENCES scf_anchors (id) ON DELETE RESTRICT,
    -- The canonical-answer text the operator wants to reuse.
    canonical_text  TEXT NOT NULL,
    -- Where this canonical answer originated. Free-form (e.g.,
    -- "Globex SIG Lite 2026-02-14"). Helps the operator trace
    -- provenance when picking a suggestion.
    source_label    TEXT NOT NULL DEFAULT '',
    -- Optional pointer back to the originating questionnaire_answer
    -- (NULL when manually added). NOT a FK because the source answer
    -- may be deleted while the canonical entry should persist.
    source_answer_id UUID NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT answer_library_text_nonempty
        CHECK (length(canonical_text) > 0)
);

-- The suggestion-lookup index. Tenant-prefixed B-tree over
-- (scf_anchor_id, updated_at DESC) keeps "give me the N most recent
-- priors for this anchor" index-only.
CREATE INDEX idx_answer_library_tenant_anchor_recent
    ON answer_library (tenant_id, scf_anchor_id, updated_at DESC);

ALTER TABLE answer_library ENABLE ROW LEVEL SECURITY;
ALTER TABLE answer_library FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON answer_library
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON answer_library
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON answer_library
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON answer_library
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON answer_library TO atlas_app;
