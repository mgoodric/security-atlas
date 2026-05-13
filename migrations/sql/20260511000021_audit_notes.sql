-- security-atlas — auditor assignments + audit notes (slice 025).
--
-- Two tables introduced:
--   auditor_assignments  - tenant-scoped, user-to-audit_period assignment.
--                          Composite PK (tenant_id, user_id, audit_period_id) --
--                          one auditor can be assigned to multiple periods
--                          (AC-6: multi-period engagements). Drives the OPA
--                          ABAC attribute `input.user.attrs.audit_period_ids`
--                          via the slice-035 attrs resolver hook.
--   audit_notes          - the auditor's private testing-notes workspace
--                          (canvas §8.1, §8.3). Auditor-only visibility for
--                          v1; `visibility` column reserved with a single
--                          allowed value `auditor_only` (the §8.5 shared
--                          auditor-auditee thread is deferred to a later
--                          slice). Scope follows canvas §8.3 enum
--                          (control / finding / sample / period).
--
-- Constitutional invariants honored:
--   #6  Tenant isolation enforced at the DB layer via RLS. Both tables use
--       the slice-014/017/018/021/026/035/036/059 four-policy split
--       (tenant_read FOR SELECT, tenant_write FOR INSERT WITH CHECK,
--       tenant_update FOR UPDATE USING + WITH CHECK, tenant_delete FOR
--       DELETE) under FORCE ROW LEVEL SECURITY.
--   #10 Audit-period freezing -- audit_notes pin to a specific audit_period_id;
--       deletion of a period would orphan notes, so the FK is ON DELETE RESTRICT.
--       Auditor reads of evidence still flow through the existing slice-026/028
--       horizon (`observed_at <= COALESCE(frozen_at, 'infinity')`); this slice
--       does NOT add a new horizon predicate.
--
-- Anti-criteria honored at the schema layer (P0):
--   - Auditor cannot mutate non-audit-notes resources: enforced at OPA + handler
--     layer (this migration adds no write surface for auditors elsewhere).
--   - Auditees cannot read auditor's notes: there is no SELECT path for the
--     audit_notes table that does not require an OPA `auditor` role allow.
--     Query layer also filters `WHERE author_user_id = $current_user` so an
--     auditor cannot read another auditor's notes within the same tenant.
--   - Auditor cannot read outside assigned period: the assignment join in
--     the attrs resolver returns only the periods the user is assigned to,
--     and the rego ABAC check denies cross-period writes.
--
-- Migration is reversible via 20260511000021_audit_notes.down.sql.

-- ===== 1. auditor_assignments =====

CREATE TABLE auditor_assignments (
    tenant_id        UUID NOT NULL,
    user_id          TEXT NOT NULL,
    audit_period_id  UUID NOT NULL,
    granted_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by       TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (tenant_id, user_id, audit_period_id),

    CONSTRAINT auditor_assignments_user_id_nonempty
        CHECK (length(user_id) > 0),

    -- Composite FK to audit_periods(tenant_id, id) blocks cross-tenant
    -- linkage. Matches slice-028's populations.audit_period_id pattern.
    -- ON DELETE RESTRICT: deleting a period with assigned auditors is
    -- refused; the operator must unassign first.
    FOREIGN KEY (tenant_id, audit_period_id)
        REFERENCES audit_periods(tenant_id, id) ON DELETE RESTRICT
);

-- Lookup by (tenant_id, user_id) is the hot path -- the AttrsResolver
-- runs this query per auditor request. The PRIMARY KEY's leading columns
-- already cover it; no extra index needed.

ALTER TABLE auditor_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE auditor_assignments FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON auditor_assignments
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON auditor_assignments
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON auditor_assignments
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON auditor_assignments
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON auditor_assignments TO atlas_app;

-- ===== 2. audit_notes =====

CREATE TABLE audit_notes (
    id               UUID PRIMARY KEY,
    tenant_id        UUID NOT NULL,
    audit_period_id  UUID NOT NULL,
    author_user_id   TEXT NOT NULL,
    scope_type       TEXT NOT NULL,
    scope_id         TEXT NULL,
    body             TEXT NOT NULL,
    visibility       TEXT NOT NULL DEFAULT 'auditor_only',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT audit_notes_scope_type_chk
        CHECK (scope_type IN ('control', 'finding', 'sample', 'period')),
    CONSTRAINT audit_notes_body_nonempty
        CHECK (length(body) > 0),
    CONSTRAINT audit_notes_author_nonempty
        CHECK (length(author_user_id) > 0),
    -- v1 ships with exactly one visibility value. The §8.5 auditor-auditee
    -- shared-thread workflow is a separate slice; until it lands, every
    -- row carries 'auditor_only' and the query layer pins it.
    CONSTRAINT audit_notes_visibility_chk
        CHECK (visibility = 'auditor_only'),

    FOREIGN KEY (tenant_id, audit_period_id)
        REFERENCES audit_periods(tenant_id, id) ON DELETE RESTRICT
);

-- Hot-path index: list notes by (tenant, author, period) -- the GET
-- /v1/audit-notes?audit_period_id=... query path.
CREATE INDEX idx_audit_notes_tenant_author_period
    ON audit_notes (tenant_id, author_user_id, audit_period_id, created_at DESC);

-- Secondary index for period-scoped scans (admin-side analytics; not used
-- by the auditor path).
CREATE INDEX idx_audit_notes_tenant_period
    ON audit_notes (tenant_id, audit_period_id, created_at DESC);

ALTER TABLE audit_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_notes FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON audit_notes
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON audit_notes
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON audit_notes
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON audit_notes
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON audit_notes TO atlas_app;
