-- security-atlas — framework_scope_applicability (issue #1787, SoA #1744).
--
-- The Statement of Applicability decisions: per requirement of a framework
-- version, whether it is `applicable` or `excluded`, with a justification
-- (required when excluded) and an implementation reference. A decision
-- belongs to one framework-scope revision (framework_scopes row); a
-- requirement with no row is undecided, and undecided means applicable.
--
-- Shape follows the CSF Profile tables (20260608080000_csf_tier_profile.sql):
-- tenant-confidential per-requirement rows over the global, frozen
-- framework_requirements catalog, with the four-policy RLS split. Framework
-- requirements are never written here.
--
-- Workflow (ADR-0001 extended to decisions):
--
--   * The scope's approval covers predicate plus decisions. Any decision
--     write on a revision in `review` or `approved` sends it back to `draft`
--     and clears its approval columns: the same reset the predicate trigger
--     (framework_scopes_bounce_on_predicate_change) performs. A decision
--     write on a `draft` revision leaves its state alone.
--   * Decisions on an `activated` or `superseded` revision are frozen: the
--     decisions in force at a point in time are exactly the rows of the
--     revision active then, so as_of reads stay honest. Change them by
--     drafting a new revision. (decisions log D3)
--   * A decision's requirement must belong to the revision's framework
--     version, and the revision must be visible under the caller's tenant.
--
-- These rules live in triggers so no caller can bypass them; the store checks
-- the same conditions first to return typed errors.
--
-- History: every decision change is written to framework_scope_workflow_log
-- as action `applicability.update` (the CHECK is extended below) by
-- internal/frameworkscope.Store, in the same tx as the change.
--
-- Constitutional invariants honored:
--   #6 Tenant isolation via RLS: ENABLE + FORCE, four-policy split on
--      current_tenant_matches(tenant_id). The parent-scope lookup in the
--      guard trigger runs under the caller's RLS, so a scope id from another
--      tenant does not resolve and the write is refused.

CREATE TABLE framework_scope_applicability (
    id                        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                 UUID        NOT NULL,
    framework_scope_id        UUID        NOT NULL
        REFERENCES framework_scopes (id) ON DELETE CASCADE,
    framework_requirement_id  UUID        NOT NULL
        REFERENCES framework_requirements (id) ON DELETE CASCADE,
    decision                  TEXT        NOT NULL
        CONSTRAINT framework_scope_applicability_decision_check
        CHECK (decision IN ('applicable', 'excluded')),
    justification             TEXT        NOT NULL DEFAULT '',
    implementation_ref        TEXT        NOT NULL DEFAULT '',
    updated_by                TEXT        NOT NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- An exclusion must be defensible to an auditor.
    CONSTRAINT framework_scope_applicability_excluded_justified
        CHECK (decision <> 'excluded' OR length(btrim(justification)) > 0),
    CONSTRAINT framework_scope_applicability_updated_by_nonempty
        CHECK (length(updated_by) > 0),
    CONSTRAINT framework_scope_applicability_unique
        UNIQUE (framework_scope_id, framework_requirement_id)
);

CREATE INDEX idx_framework_scope_applicability_tenant_scope
    ON framework_scope_applicability (tenant_id, framework_scope_id);

ALTER TABLE framework_scope_applicability ENABLE ROW LEVEL SECURITY;
ALTER TABLE framework_scope_applicability FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON framework_scope_applicability
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON framework_scope_applicability
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON framework_scope_applicability
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON framework_scope_applicability
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON framework_scope_applicability TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON framework_scope_applicability TO atlas_migrate;

-- ===== guard: frozen revisions, requirement/version match, tenant =====

CREATE OR REPLACE FUNCTION framework_scope_applicability_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    scope_state  TEXT;
    scope_fv     UUID;
    scope_tenant UUID;
BEGIN
    SELECT state, framework_version_id, tenant_id
      INTO scope_state, scope_fv, scope_tenant
      FROM framework_scopes
     WHERE id = NEW.framework_scope_id;
    IF NOT FOUND OR scope_tenant <> NEW.tenant_id THEN
        RAISE EXCEPTION 'framework_scope_applicability: framework scope % not found for tenant', NEW.framework_scope_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF scope_state IN ('activated', 'superseded') THEN
        RAISE EXCEPTION 'framework_scope_applicability: decisions on a % framework scope are frozen', scope_state
            USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM framework_requirements
         WHERE id = NEW.framework_requirement_id
           AND framework_version_id = scope_fv
    ) THEN
        RAISE EXCEPTION 'framework_scope_applicability: requirement % is not in the scope''s framework version', NEW.framework_requirement_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END;
$$;

-- INSERT and UPDATE only. No API deletes a decision; DELETE is left to the
-- ON DELETE CASCADE from framework_scopes / framework_requirements and to
-- the operator purge (internal/auth/oauthclient), which a frozen-revision
-- guard would otherwise block. (decisions log D4)
CREATE TRIGGER framework_scope_applicability_guard_trg
    BEFORE INSERT OR UPDATE ON framework_scope_applicability
    FOR EACH ROW
    EXECUTE FUNCTION framework_scope_applicability_guard();

-- ===== reset: a decision change on review/approved sends the scope to draft =====

CREATE OR REPLACE FUNCTION framework_scope_applicability_reset_approval()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    scope_id UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        scope_id := OLD.framework_scope_id;
    ELSE
        scope_id := NEW.framework_scope_id;
    END IF;

    UPDATE framework_scopes
       SET state = 'draft',
           approver_user_id = NULL,
           approved_at = NULL,
           predicate_hash_at_approval = NULL,
           approval_evidence_file_url = NULL,
           approval_evidence_file_hash = NULL,
           updated_at = now()
     WHERE id = scope_id
       AND state IN ('review', 'approved');

    RETURN NULL;
END;
$$;

CREATE TRIGGER framework_scope_applicability_reset_approval_trg
    AFTER INSERT OR UPDATE OR DELETE ON framework_scope_applicability
    FOR EACH ROW
    EXECUTE FUNCTION framework_scope_applicability_reset_approval();

-- ===== workflow log: add the applicability action =====

ALTER TABLE framework_scope_workflow_log
    DROP CONSTRAINT framework_scope_workflow_log_action_check;
ALTER TABLE framework_scope_workflow_log
    ADD CONSTRAINT framework_scope_workflow_log_action_check
    CHECK (action IN ('create', 'predicate.update', 'applicability.update', 'submit', 'approve', 'activate', 'supersede'));
