-- Slice 758 — questionnaire export audit trail.
--
-- Every questionnaire export is an audit-binding publish attempt. This table
-- records the actor, questionnaire, timestamp, and approval-gate counts so the
-- platform can answer exactly what was allowed through the export boundary.
-- Append-only by RLS shape: SELECT + INSERT policies only.

CREATE TABLE questionnaire_export_audit_log (
    id                      UUID PRIMARY KEY,
    tenant_id               UUID NOT NULL,
    questionnaire_id        UUID NOT NULL,
    actor                   TEXT NOT NULL,
    manual_count            INTEGER NOT NULL,
    approved_ai_count       INTEGER NOT NULL,
    excluded_draft_count    INTEGER NOT NULL,
    exported_answer_count   INTEGER NOT NULL,
    occurred_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    subject_module          TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT questionnaire_export_audit_actor_nonempty
        CHECK (length(actor) > 0),
    CONSTRAINT questionnaire_export_audit_counts_nonnegative
        CHECK (
            manual_count >= 0
            AND approved_ai_count >= 0
            AND excluded_draft_count >= 0
            AND exported_answer_count >= 0
        ),
    FOREIGN KEY (questionnaire_id)
        REFERENCES questionnaires(id) ON DELETE RESTRICT
);

CREATE INDEX idx_questionnaire_export_audit_tenant_occurred
    ON questionnaire_export_audit_log (tenant_id, occurred_at DESC);

CREATE INDEX idx_questionnaire_export_audit_tenant_questionnaire
    ON questionnaire_export_audit_log (tenant_id, questionnaire_id, occurred_at DESC);

ALTER TABLE questionnaire_export_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaire_export_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON questionnaire_export_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON questionnaire_export_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON questionnaire_export_audit_log TO atlas_app;
GRANT SELECT ON questionnaire_export_audit_log TO atlas_service_account;
