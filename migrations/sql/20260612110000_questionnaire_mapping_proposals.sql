-- security-atlas — slice 755: questionnaire SCF mapping proposals.
--
-- Adds tenant-scoped storage for AI-suggested mappings from an unmapped
-- questionnaire question to the canonical SCF catalog. A proposal is always
-- ai_assisted=TRUE and human_approved=FALSE when created; only the explicit
-- approve endpoint records human_approver and writes
-- questionnaire_questions.scf_anchor_id.

DO $$ BEGIN
    ALTER TABLE questionnaire_questions
        ADD CONSTRAINT questionnaire_questions_tenant_id_id_unique
        UNIQUE (tenant_id, id);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS questionnaire_mapping_proposals (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL,
    question_id          UUID NOT NULL,
    scf_anchor_uuid      UUID NOT NULL REFERENCES scf_anchors(id),
    scf_anchor_id        TEXT NOT NULL,
    scf_anchor_title     TEXT NOT NULL,
    rationale            TEXT NOT NULL,
    candidate_anchor_ids JSONB NOT NULL DEFAULT '[]'::JSONB,
    status               TEXT NOT NULL DEFAULT 'pending',

    ai_assisted          BOOLEAN NOT NULL DEFAULT TRUE,
    human_approved       BOOLEAN NOT NULL DEFAULT FALSE,
    human_approver       TEXT NULL,

    prompt_version       TEXT NOT NULL,
    model_name           TEXT NOT NULL,
    model_version        TEXT NOT NULL,
    model_provider       TEXT NOT NULL,

    created_by           TEXT NOT NULL DEFAULT '',
    approved_at          TIMESTAMPTZ NULL,
    rejected_at          TIMESTAMPTZ NULL,
    reject_reason        TEXT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT qmp_status_chk
        CHECK (status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT qmp_ai_assist_invariant
        CHECK (ai_assist_human_approver_guard(
            ai_assisted, human_approved, human_approver)),
    CONSTRAINT qmp_ai_provenance_nonempty
        CHECK (
            NOT ai_assisted
            OR (
                length(prompt_version) > 0
                AND length(model_name) > 0
                AND length(model_version) > 0
                AND length(model_provider) > 0
            )
        ),
    CONSTRAINT qmp_pending_consistency
        CHECK (
            status <> 'pending'
            OR (human_approved = FALSE AND human_approver IS NULL
                AND approved_at IS NULL AND rejected_at IS NULL)
        ),
    CONSTRAINT qmp_approved_consistency
        CHECK (
            status <> 'approved'
            OR (human_approved = TRUE AND human_approver IS NOT NULL
                AND approved_at IS NOT NULL AND rejected_at IS NULL)
        ),
    CONSTRAINT qmp_rejected_consistency
        CHECK (
            status <> 'rejected'
            OR (human_approved = FALSE AND approved_at IS NULL
                AND rejected_at IS NOT NULL)
        ),
    CONSTRAINT qmp_question_tenant_fk
        FOREIGN KEY (tenant_id, question_id)
        REFERENCES questionnaire_questions (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qmp_tenant_question_status
    ON questionnaire_mapping_proposals (tenant_id, question_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_qmp_tenant_status_created
    ON questionnaire_mapping_proposals (tenant_id, status, created_at DESC);

ALTER TABLE questionnaire_mapping_proposals ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_mapping_proposals FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_read ON questionnaire_mapping_proposals
        FOR SELECT
        USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
    CREATE POLICY tenant_write ON questionnaire_mapping_proposals
        FOR INSERT
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
    CREATE POLICY tenant_update ON questionnaire_mapping_proposals
        FOR UPDATE
        USING (current_tenant_matches(tenant_id))
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
    CREATE POLICY tenant_delete ON questionnaire_mapping_proposals
        FOR DELETE
        USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON questionnaire_mapping_proposals TO atlas_app;

CREATE TABLE IF NOT EXISTS questionnaire_mapping_proposal_audit (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL,
    proposal_id    UUID NULL REFERENCES questionnaire_mapping_proposals(id) ON DELETE SET NULL,
    question_id    UUID NOT NULL,
    actor          TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL,
    prompt_version TEXT NOT NULL DEFAULT '',
    model_name     TEXT NOT NULL DEFAULT '',
    model_version  TEXT NOT NULL DEFAULT '',
    model_provider TEXT NOT NULL DEFAULT '',
    payload_json   JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT qmp_audit_action_chk
        CHECK (action IN ('suggested', 'approved', 'rejected', 'suppressed')),
    CONSTRAINT qmp_audit_question_tenant_fk
        FOREIGN KEY (tenant_id, question_id)
        REFERENCES questionnaire_questions (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qmp_audit_tenant_question_created
    ON questionnaire_mapping_proposal_audit (tenant_id, question_id, created_at DESC);

ALTER TABLE questionnaire_mapping_proposal_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_mapping_proposal_audit FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_read ON questionnaire_mapping_proposal_audit
        FOR SELECT
        USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
    CREATE POLICY tenant_write ON questionnaire_mapping_proposal_audit
        FOR INSERT
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

GRANT SELECT, INSERT ON questionnaire_mapping_proposal_audit TO atlas_app;
