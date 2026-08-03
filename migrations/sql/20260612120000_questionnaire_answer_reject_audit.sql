-- security-atlas — slice 757: questionnaire AI-draft reject audit.
--
-- The batch review queue (slice 757) adds the one new write surface of the
-- questionnaire AI-assist family: POST .../answers/{qid}/ai-reject discards an
-- UNAPPROVED AI draft so the question returns to unanswered. Because the draft
-- row is deleted, the rejection event needs its own append-only record — this
-- table. It mirrors questionnaire_mapping_proposal_audit (slice 755): actor,
-- action, snapshot-at-rejection model provenance, fixed-vocabulary payload.
--
-- answer_id deliberately carries NO foreign key: the audited row is deleted in
-- the same transaction that writes this record, so any FK (even ON DELETE SET
-- NULL) would erase the very id the audit exists to preserve.
--
-- Constitutional invariants honored:
--   AI-assist boundary (hard): reject applies ONLY to unapproved AI drafts —
--     the store predicate refuses approved or manual answers (P0-757-4);
--     deleting approved content stays a different, deliberate operation.
--   #6 Tenant isolation: FORCE RLS + current_tenant_matches on every policy.
--     Read + insert only — an audit row is never updated or deleted by the app.

CREATE TABLE IF NOT EXISTS questionnaire_answer_reject_audit (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL,
    answer_id      UUID NOT NULL,
    question_id    UUID NOT NULL,
    actor          TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL DEFAULT 'rejected',
    prompt_version TEXT NOT NULL DEFAULT '',
    model_name     TEXT NOT NULL DEFAULT '',
    model_version  TEXT NOT NULL DEFAULT '',
    model_provider TEXT NOT NULL DEFAULT '',
    payload_json   JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT qara_action_chk
        CHECK (action IN ('rejected')),
    CONSTRAINT qara_question_tenant_fk
        FOREIGN KEY (tenant_id, question_id)
        REFERENCES questionnaire_questions (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qara_tenant_question_created
    ON questionnaire_answer_reject_audit (tenant_id, question_id, created_at DESC);

ALTER TABLE questionnaire_answer_reject_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_answer_reject_audit FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_read ON questionnaire_answer_reject_audit
        FOR SELECT
        USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
    CREATE POLICY tenant_write ON questionnaire_answer_reject_audit
        FOR INSERT
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

GRANT SELECT, INSERT ON questionnaire_answer_reject_audit TO atlas_app;
