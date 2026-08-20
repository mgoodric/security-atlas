-- Slice 756 -- Questionnaire batch answer-drafting runs.
--
-- A run is a tenant-scoped, auditable batch driver over the existing
-- qaisuggest.Service.Suggest single-row contract. The run tables are the
-- record of orchestration only; produced drafts remain in questionnaire_answers
-- as unapproved AI-assisted drafts.

CREATE TABLE IF NOT EXISTS questionnaire_answer_runs (
    id                      UUID PRIMARY KEY,
    tenant_id               UUID NOT NULL,
    questionnaire_id        UUID NOT NULL REFERENCES questionnaires (id) ON DELETE CASCADE,
    status                  TEXT NOT NULL DEFAULT 'pending',
    started_by              TEXT NOT NULL DEFAULT '',
    row_cap                 INTEGER NOT NULL,
    total_count             INTEGER NOT NULL DEFAULT 0,
    drafted_count           INTEGER NOT NULL DEFAULT 0,
    insufficient_count      INTEGER NOT NULL DEFAULT 0,
    suppressed_count        INTEGER NOT NULL DEFAULT 0,
    skipped_count           INTEGER NOT NULL DEFAULT 0,
    error_count             INTEGER NOT NULL DEFAULT 0,
    started_at              TIMESTAMPTZ NULL,
    completed_at            TIMESTAMPTZ NULL,
    failed_at               TIMESTAMPTZ NULL,
    canceled_at             TIMESTAMPTZ NULL,
    failure_reason          TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT questionnaire_answer_runs_status_chk
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'canceled')),
    CONSTRAINT questionnaire_answer_runs_row_cap_positive
        CHECK (row_cap > 0),
    CONSTRAINT questionnaire_answer_runs_counts_nonnegative
        CHECK (
            total_count >= 0
            AND drafted_count >= 0
            AND insufficient_count >= 0
            AND suppressed_count >= 0
            AND skipped_count >= 0
            AND error_count >= 0
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_questionnaire_answer_runs_one_active
    ON questionnaire_answer_runs (tenant_id, questionnaire_id)
    WHERE status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_questionnaire_answer_runs_tenant_qn_created
    ON questionnaire_answer_runs (tenant_id, questionnaire_id, created_at DESC);

CREATE TABLE IF NOT EXISTS questionnaire_answer_run_items (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    run_id              UUID NOT NULL REFERENCES questionnaire_answer_runs (id) ON DELETE CASCADE,
    questionnaire_id    UUID NOT NULL REFERENCES questionnaires (id) ON DELETE CASCADE,
    question_id         UUID NOT NULL REFERENCES questionnaire_questions (id) ON DELETE CASCADE,
    sort_order          INTEGER NOT NULL DEFAULT 0,
    outcome             TEXT NOT NULL,
    reason_code         TEXT NULL,
    answer_id           UUID NULL REFERENCES questionnaire_answers (id) ON DELETE SET NULL,
    error_message       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT questionnaire_answer_run_items_outcome_chk
        CHECK (outcome IN (
            'drafted',
            'insufficient_evidence',
            'suppressed',
            'skipped_needs_mapping',
            'skipped_already_answered',
            'error'
        )),
    CONSTRAINT questionnaire_answer_run_items_reason_chk
        CHECK (
            reason_code IS NULL
            OR reason_code IN (
                'insufficient_evidence',
                'generation_unavailable',
                'unresolved_citation',
                'no_citations',
                'needs_mapping',
                'already_answered',
                'row_cap_exceeded',
                'suggest_error',
                'run_canceled'
            )
        ),
    CONSTRAINT questionnaire_answer_run_items_answer_shape_chk
        CHECK (
            (outcome = 'drafted' AND answer_id IS NOT NULL)
            OR (outcome <> 'drafted' AND answer_id IS NULL)
        ),
    CONSTRAINT questionnaire_answer_run_items_unique_question_per_run
        UNIQUE (run_id, question_id)
);

CREATE INDEX IF NOT EXISTS idx_questionnaire_answer_run_items_tenant_run_sort
    ON questionnaire_answer_run_items (tenant_id, run_id, sort_order);

CREATE OR REPLACE FUNCTION questionnaire_answer_run_status_transition_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.status IS DISTINCT FROM OLD.status THEN
        IF NOT (
            (OLD.status = 'pending' AND NEW.status IN ('running', 'canceled'))
            OR (OLD.status = 'running' AND NEW.status IN ('completed', 'failed', 'canceled'))
        ) THEN
            RAISE EXCEPTION 'illegal questionnaire_answer_runs status transition: % -> %', OLD.status, NEW.status
                USING ERRCODE = '23514';
        END IF;
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_questionnaire_answer_run_status_transition
    ON questionnaire_answer_runs;
CREATE TRIGGER trg_questionnaire_answer_run_status_transition
    BEFORE UPDATE ON questionnaire_answer_runs
    FOR EACH ROW
    EXECUTE FUNCTION questionnaire_answer_run_status_transition_guard();

ALTER TABLE questionnaire_answer_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_answer_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_read ON questionnaire_answer_runs;
DROP POLICY IF EXISTS tenant_write ON questionnaire_answer_runs;
DROP POLICY IF EXISTS tenant_update ON questionnaire_answer_runs;
DROP POLICY IF EXISTS tenant_delete ON questionnaire_answer_runs;
CREATE POLICY tenant_read ON questionnaire_answer_runs
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON questionnaire_answer_runs
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON questionnaire_answer_runs
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON questionnaire_answer_runs
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

ALTER TABLE questionnaire_answer_run_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_answer_run_items FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_read ON questionnaire_answer_run_items;
DROP POLICY IF EXISTS tenant_write ON questionnaire_answer_run_items;
DROP POLICY IF EXISTS tenant_update ON questionnaire_answer_run_items;
DROP POLICY IF EXISTS tenant_delete ON questionnaire_answer_run_items;
CREATE POLICY tenant_read ON questionnaire_answer_run_items
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON questionnaire_answer_run_items
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON questionnaire_answer_run_items
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON questionnaire_answer_run_items
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON questionnaire_answer_runs TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON questionnaire_answer_run_items TO atlas_app;
