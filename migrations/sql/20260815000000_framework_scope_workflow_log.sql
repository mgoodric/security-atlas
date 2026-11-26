-- security-atlas — framework_scope_workflow_log (issue #1786, SoA prefactor).
--
-- The append-only history log for framework scopes that slice 018 planned
-- and slice 062 anticipated (its admin_audit_log_v header notes the table
-- "does NOT exist on main"). One row per framework_scopes change:
--
--   create            a draft is inserted                    (null -> draft)
--   predicate.update  the predicate is edited; a review or
--                     approved row resets to draft here      (X -> X | draft)
--   submit            draft -> review
--   approve           review -> approved
--   activate          approved -> activated
--   supersede         activated -> superseded (the predecessor, written in
--                     the same tx as its successor's activate)
--
-- before / after are the framework_scopes row as JSON on either side of the
-- change (before is NULL on create). actor is the calling credential id,
-- matching exception_audit_log. Later SoA tickets add applicability actions
-- to this same table by extending the action CHECK.
--
-- Rows are written by internal/frameworkscope.Store in the same transaction
-- as the change they record, so a rolled-back transition leaves no row.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via RLS: FORCE RLS, tenant_read + tenant_write only.
--      No UPDATE / DELETE policy and no UPDATE / DELETE grant to atlas_app,
--      so the app role cannot rewrite or remove history.
--
-- framework_scope_id carries no FK: the log outlives the row it describes
-- (the same choice exception_audit_log and walkthrough_audit_log make).

CREATE TABLE framework_scope_workflow_log (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL,
    framework_scope_id  UUID        NOT NULL,
    action              TEXT        NOT NULL
        CONSTRAINT framework_scope_workflow_log_action_check
        CHECK (action IN ('create', 'predicate.update', 'submit', 'approve', 'activate', 'supersede')),
    from_state          TEXT        NULL,
    to_state            TEXT        NOT NULL,
    before              JSONB       NULL,
    after               JSONB       NOT NULL,
    actor               TEXT        NOT NULL,
    occurred_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    subject_module      TEXT        NOT NULL DEFAULT 'core'
);

CREATE INDEX idx_framework_scope_workflow_log_tenant_occurred
    ON framework_scope_workflow_log (tenant_id, occurred_at DESC);
CREATE INDEX idx_framework_scope_workflow_log_scope
    ON framework_scope_workflow_log (tenant_id, framework_scope_id, occurred_at);

ALTER TABLE framework_scope_workflow_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE framework_scope_workflow_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON framework_scope_workflow_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON framework_scope_workflow_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update/delete policies — append-only.

GRANT SELECT, INSERT ON framework_scope_workflow_log TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON framework_scope_workflow_log TO atlas_migrate;
