-- security-atlas — RBAC roles + decision audit log (slice 035).
--
-- Adds two tables:
--
--   user_roles            - per-tenant user-to-role membership. Composite PK
--                           (tenant_id, user_id, role) — one user can hold
--                           multiple roles per tenant. Role is constrained to
--                           the 5 canonical roles from canvas §9.5.
--   decision_audit_log    - append-only record of every authz decision
--                           (allow or deny). One row per call to
--                           authz.Decide. RLS allows SELECT + INSERT only
--                           (no UPDATE / DELETE policy) under FORCE — the
--                           append-only pattern from slices 013 / 011 / 026.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation enforced at the database layer via RLS.
--       user_roles uses the slice-011/017/018/021/036 four-policy split.
--       decision_audit_log uses the slice-013 append-only two-policy split
--       (tenant_read FOR SELECT + tenant_write FOR INSERT WITH CHECK; no
--       UPDATE / DELETE policies; FORCE ROW LEVEL SECURITY).
--
-- Canvas §9.5 anchors the 5-role enum and the "fine cuts" ABAC pattern.
-- The Rego policies live under policies/authz/ and reference user_roles for
-- the RBAC half + scope-cell / audit-period attribute predicates for the
-- ABAC half.
--
-- Anti-criteria honored at the schema layer (P0):
--   - No emergency-bypass role: the CHECK constraint enumerates exactly the
--     five canonical roles. No 'bypass', no 'superadmin', no escape hatch.
--   - Audit log is append-only by construction: the absence of UPDATE +
--     DELETE policies on decision_audit_log means atlas_app cannot mutate
--     prior decisions even if a future bug tried.
--
-- HITL gate: the 5-role enum + the ~10 seed Rego policies under
-- policies/authz/ ship as community_draft attribution. Orchestrator + user
-- pair-review pre-merge per docs/audit-log/authz-review.md.
--
-- Migration is reversible via 20260511000018_rbac_authz.down.sql.

-- ===== 1. user_roles =====

CREATE TABLE user_roles (
    tenant_id    UUID NOT NULL,
    user_id      TEXT NOT NULL,
    role         TEXT NOT NULL,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by   TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (tenant_id, user_id, role),

    -- Canvas §9.5 locks the 5-role enum. Future role additions must
    -- update this CHECK and add a matching .rego file under policies/authz/.
    CONSTRAINT user_roles_role_chk
        CHECK (role IN ('admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer')),
    CONSTRAINT user_roles_user_id_nonempty
        CHECK (length(user_id) > 0)
);

-- Lookup by (tenant_id, user_id) is the hot path -- the authz middleware
-- runs this query per request. The PRIMARY KEY's leading columns already
-- cover it; no extra index needed.

ALTER TABLE user_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_roles FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON user_roles
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON user_roles
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON user_roles
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON user_roles
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON user_roles TO atlas_app;

-- ===== 2. decision_audit_log =====
--
-- Append-only by RLS construction: the SELECT + INSERT policies are the
-- only ones installed; UPDATE + DELETE are denied for atlas_app because
-- FORCE ROW LEVEL SECURITY rejects commands without a matching policy.
-- Mirrors slice 013's evidence_audit_log + slice 011's
-- exception_audit_log + slice 026's sample_audit_log pattern.

CREATE TABLE decision_audit_log (
    decision_id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id       TEXT NOT NULL,
    user_roles    TEXT[] NOT NULL DEFAULT '{}',
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL DEFAULT '',
    result        TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    policy_hits   TEXT[] NOT NULL DEFAULT '{}',
    request_path  TEXT NOT NULL DEFAULT '',
    request_method TEXT NOT NULL DEFAULT '',

    CONSTRAINT decision_audit_log_result_chk
        CHECK (result IN ('allow', 'deny')),
    CONSTRAINT decision_audit_log_user_id_nonempty
        CHECK (length(user_id) > 0),
    CONSTRAINT decision_audit_log_action_nonempty
        CHECK (length(action) > 0),
    CONSTRAINT decision_audit_log_resource_type_nonempty
        CHECK (length(resource_type) > 0)
);

CREATE INDEX idx_decision_audit_log_tenant_occurred
    ON decision_audit_log (tenant_id, occurred_at DESC);

CREATE INDEX idx_decision_audit_log_tenant_user_occurred
    ON decision_audit_log (tenant_id, user_id, occurred_at DESC);

ALTER TABLE decision_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE decision_audit_log FORCE ROW LEVEL SECURITY;

-- SELECT + INSERT only -- no UPDATE / DELETE policy. Under FORCE ROW LEVEL
-- SECURITY this means atlas_app can read its tenant's rows and append new
-- rows, but cannot mutate or delete prior decisions.
CREATE POLICY tenant_read ON decision_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decision_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON decision_audit_log TO atlas_app;
