-- security-atlas — control_manifest_revisions (issue #1908).
--
-- A control.yaml re-upload that changes only the top-level `description`
-- updates the active controls row in place instead of superseding it, so
-- owners, evidence, exceptions and links keep their control id. This table
-- keeps the prose history that the in-place update would otherwise lose:
-- one row per in-place update, holding the manifest as it was just before
-- the overwrite.
--
-- Rows are written by internal/control.Store.Upload in the same transaction
-- as the UPDATE they precede, so a rolled-back upload leaves no row. Every
-- other content change still supersedes and keeps its prior version as a
-- controls row, so it writes nothing here.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via RLS: FORCE RLS, tenant_read + tenant_write only.
--      No UPDATE / DELETE policy and no UPDATE / DELETE grant to atlas_app,
--      so the app role cannot rewrite or remove history.
--
-- control_id carries no FK: the log outlives the row it describes (the same
-- choice framework_scope_workflow_log and exception_audit_log make).

CREATE TABLE control_manifest_revisions (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID        NOT NULL,
    control_id            UUID        NOT NULL,
    bundle_manifest_yaml  TEXT        NOT NULL,
    bundle_manifest_hash  TEXT        NOT NULL,
    bundle_uploaded_by    TEXT        NULL,
    bundle_uploaded_at    TIMESTAMPTZ NULL,
    replaced_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_control_manifest_revisions_control
    ON control_manifest_revisions (tenant_id, control_id, replaced_at);

ALTER TABLE control_manifest_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_manifest_revisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON control_manifest_revisions
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON control_manifest_revisions
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update/delete policies — append-only.

GRANT SELECT, INSERT ON control_manifest_revisions TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON control_manifest_revisions TO atlas_migrate;
