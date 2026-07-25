-- security-atlas — slice 468: server-backed control owner-assignment +
-- saved filter-views (controls list).
--
-- Backend completion of slice 448's frontend shell. Three tables:
--
--   control_owner_assignments       — the per-control owner-USER assignment
--                                      the single-item assign path writes and
--                                      the bulk path re-checks per item. This
--                                      is the central JUDGMENT model (decisions
--                                      log 468 D1): an ADDITIVE owner-USER FK
--                                      assignment that does NOT touch the
--                                      existing read-only `controls.owner_role`
--                                      RACI string. owner_role stays the
--                                      canvas §2 "who owns it (RACI)" role
--                                      label; this table is the concrete
--                                      person accountable today.
--
--   control_owner_assignment_audit_log
--                                   — append-only repudiation ledger
--                                      (threat-model R / P0-448-4). ONE row per
--                                      assign event; the bulk path writes one
--                                      row whose control_ids[] references the
--                                      whole affected set (the "one bulk event
--                                      referencing N items" shape the spec
--                                      permits). A later "who reassigned all
--                                      these?" question is answerable.
--
--   saved_views                     — per-(tenant, user) saved filter-views
--                                      (threat-model I / P0-448-5). RLS scopes
--                                      the tenant half at the DB layer
--                                      (invariant #6); the per-USER cut is
--                                      enforced at the application layer via a
--                                      mandatory user_id predicate sourced from
--                                      the verified credential (never the
--                                      request body) — the exact shape
--                                      user_notification_preferences
--                                      (slice 016 me-endpoints) established,
--                                      because v1 has only an `app.current_tenant`
--                                      GUC, no `app.current_user` GUC.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation enforced at the DB layer via RLS. All three tables
--       use current_tenant_matches(tenant_id). control_owner_assignments +
--       saved_views use the slice-011/017/018/036 four-policy split (read /
--       write / update / delete). control_owner_assignment_audit_log uses the
--       slice-013/062 append-only two-policy split (SELECT + INSERT only; no
--       UPDATE / DELETE policy under FORCE ROW LEVEL SECURITY).
--   Repudiation discipline — the audit log is append-only by construction.
--
-- Reversible via 20260612060000_control_owner_assign_saved_views.down.sql.

-- ===== 1. control_owner_assignments =====
--
-- One row per (tenant_id, control_id) — a control has at most one assigned
-- owner-user at a time (re-assigning UPSERTs). owner_user_id is a plain UUID
-- validated in the handler against a tenant-GUC read of `users` (RLS hides
-- cross-tenant users → "owner is not a tenant user", threat-model T); we do
-- NOT add a composite FK to users because users carries no UNIQUE(tenant_id,
-- id) target and the handler-side validation is the tenant boundary the
-- attest path (slice 011) already established.
--
-- The (tenant_id, control_id) FK targets controls' UNIQUE (tenant_id, id)
-- (slice 002 D3 cross-tenant-safe FK target) so a row can never reference a
-- control in another tenant even with RLS momentarily disabled.

CREATE TABLE control_owner_assignments (
    tenant_id     UUID NOT NULL,
    control_id    UUID NOT NULL,
    owner_user_id UUID NOT NULL,
    assigned_by   UUID NOT NULL,
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, control_id),

    CONSTRAINT control_owner_assignments_control_fk
        FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id)
        ON DELETE CASCADE
);

CREATE INDEX control_owner_assignments_owner
    ON control_owner_assignments (tenant_id, owner_user_id);

ALTER TABLE control_owner_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_owner_assignments FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON control_owner_assignments
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON control_owner_assignments
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON control_owner_assignments
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON control_owner_assignments
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON control_owner_assignments TO atlas_app;

-- ===== 2. control_owner_assignment_audit_log =====
--
-- Append-only repudiation ledger. ONE row per assign event. For a single-item
-- assign, control_ids carries the one id; for a bulk assign, it carries the
-- whole applied set (threat-model R). Append-only by RLS construction: only
-- SELECT + INSERT policies are installed; UPDATE + DELETE are denied for
-- atlas_app under FORCE ROW LEVEL SECURITY.

CREATE TABLE control_owner_assignment_audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id UUID NOT NULL,
    owner_user_id UUID NOT NULL,
    control_ids   UUID[] NOT NULL DEFAULT '{}',
    is_bulk       BOOLEAN NOT NULL DEFAULT false,

    CONSTRAINT control_owner_assignment_audit_log_control_ids_nonempty
        CHECK (cardinality(control_ids) > 0)
);

CREATE INDEX control_owner_assignment_audit_log_tenant_occurred
    ON control_owner_assignment_audit_log (tenant_id, occurred_at DESC);

ALTER TABLE control_owner_assignment_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_owner_assignment_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON control_owner_assignment_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON control_owner_assignment_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update / delete policies — append-only.

GRANT SELECT, INSERT ON control_owner_assignment_audit_log TO atlas_app;

-- ===== 3. saved_views =====
--
-- Per-(tenant, user) saved filter-views for the /controls list. surface
-- distinguishes which list the view belongs to (v1 ships only 'controls';
-- the column is forward-room for the evidence/risks/policies follow-ons —
-- slice 448 P0-448-7 keeps THIS slice controls-only, so the CHECK pins it).
--
-- filters is the validated filter-criteria payload (threat-model T): the
-- handler narrows it to the known slice-224 controls-filter keys before
-- INSERT, so no arbitrary JSON round-trips into a query. name is unique
-- per (tenant, user, surface) case-insensitively via the lower(name) index.

CREATE TABLE saved_views (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    user_id     UUID NOT NULL,
    surface     TEXT NOT NULL DEFAULT 'controls'
                CHECK (surface IN ('controls')),
    name        TEXT NOT NULL,
    filters     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT saved_views_name_nonempty CHECK (length(btrim(name)) > 0),
    CONSTRAINT saved_views_name_len CHECK (length(name) <= 60)
);

-- Per-user uniqueness (case-insensitive) so "Triage" and "triage" cannot
-- both exist for one user — mirrors the slice-448 client addView contract.
CREATE UNIQUE INDEX saved_views_name_unique
    ON saved_views (tenant_id, user_id, surface, lower(name));

CREATE INDEX saved_views_tenant_user
    ON saved_views (tenant_id, user_id, surface, created_at);

ALTER TABLE saved_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_views FORCE ROW LEVEL SECURITY;

-- RLS scopes the TENANT half (invariant #6). The per-USER cut is enforced
-- by the handler's mandatory user_id predicate (sourced from the verified
-- credential, never the body) — there is no app.current_user GUC at v1, so
-- this follows the user_notification_preferences precedent exactly.
CREATE POLICY tenant_read ON saved_views
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON saved_views
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON saved_views
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON saved_views
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON saved_views TO atlas_app;
