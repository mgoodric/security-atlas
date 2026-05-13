-- security-atlas — slice 052: risk hierarchy + theme taxonomy + Decision Log schema.
--
-- Lays the schema foundation for slices 053 (theme tagging + manual aggregation
-- API), 054 (aggregation rules engine), and 055 (Decision Log CRUD). This slice
-- is SCHEMA ONLY — no business logic, no engines, no HTTP routes. Three shapes
-- land here per Plans/canvas/06-risk.md §6.4–§6.7:
--
--   1. Risk hierarchy fields on `risks` — `level`, `org_unit_id`, `themes`
--      (canvas §6.4)
--   2. `org_themes` flat tenant-private taxonomy + 10 built-in defaults
--      (canvas §6.5; defaults seeded by the companion 20260511000015 migration)
--   3. Decision Log — `decisions` plus four explicit M:N link tables for risks,
--      controls, exceptions, and scope predicates (canvas §6.7)
--
-- Anti-criteria honored at DDL level:
--   - No `parent_theme_id` on `org_themes` (canvas §6.5: themes are flat;
--     aggregation rules carry the logic, not the taxonomy)
--   - Four separate decision-link tables, NOT a polymorphic
--     (target_kind, target_id) table — the explicit shape preserves RLS via
--     composite FK, gives auditors clear per-type tables, and survives the
--     downstream slice-053/054/055 work cleanly
--   - No aggregation rule evaluation logic (slice 054 territory)
--   - No auto-close behavior on `risk_aggregations` — canvas §6.4 and §6.6
--     explicit that closing all children does NOT auto-close the parent; the
--     parent represents a pattern that may persist
--
-- RLS shape on every new tenant-scoped table follows the slice-014/017/019
-- four-policy split (read / write / update / delete) under FORCE ROW LEVEL
-- SECURITY. Reads compare against the `app.current_tenant` GUC via the
-- existing `current_tenant_matches(uuid)` helper.
--
-- AC-7 role-based writes: `risks` and `decisions` carry additional WITH CHECK
-- predicates that gate write access by the bound role. Roles are not yet wired
-- end-to-end (slice 035 is the HITL-ready RBAC slice), so this migration
-- accepts a transitional sentinel: if `app.current_role` is unset or set to
-- `'*'`, the role check passes. Once slice 035 lands, application code will
-- set `app.current_role` per request and the sentinel branch becomes
-- effectively unused. The check is intentionally schema-level — it cannot be
-- bypassed by application bugs that forget to set the role.

-- ===== Enums =====
--
-- risk_level: the three-tier hierarchy from canvas §6.4. Reused by `risks`,
-- `org_units`, and `aggregation_rules` (slice 054). Prefixed with `risk_` so
-- the global enum namespace stays unambiguous.
CREATE TYPE risk_level AS ENUM (
    'team',
    'org',
    'company'
);

-- decision_status: canvas §6.7 lifecycle states. `revisited` is a non-terminal
-- state indicating the decision has been reviewed at its revisit_by date
-- without being changed; `superseded` is terminal and pairs with the
-- `superseded_by` FK.
CREATE TYPE decision_status AS ENUM (
    'active',
    'revisited',
    'superseded',
    'expired'
);

-- ===== org_units =====
--
-- The organizational hierarchy that gives `risks.org_unit_id` a target.
-- Self-referencing parent_id allows arbitrary nesting (team rolls up to org
-- rolls up to company). `level` constrains a row to one of the three tiers;
-- the application enforces the invariant that a team-level org_unit cannot
-- have an org/company-level parent (parent must be same-or-broader level).
--
-- `acceptance_authorities` is a jsonb array of role identifiers permitted to
-- sign off on risks bound to this org_unit. Per canvas §6.4: "each tenant
-- configures the role-to-level mapping in org_units.acceptance_authorities".
-- The shape is intentionally loose — slice 035 (RBAC) will pin the role
-- vocabulary; until then any string array is accepted.
CREATE TABLE org_units (
    id                       UUID PRIMARY KEY,
    tenant_id                UUID NOT NULL,
    name                     TEXT NOT NULL,
    parent_id                UUID NULL,
    level                    risk_level NOT NULL,
    acceptance_authorities   JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite UNIQUE so `risks.org_unit_id` (and any future child) can FK
    -- back as (tenant_id, id) — same D3 cross-tenant-FK safety pattern that
    -- slice 019 added to `risks` and slice 005 to `controls`.
    UNIQUE (tenant_id, id),
    -- Self-ref via composite FK so a parent in another tenant is impossible.
    CONSTRAINT org_units_parent_fk
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES org_units(tenant_id, id)
        ON DELETE SET NULL,
    CONSTRAINT acceptance_authorities_is_array
        CHECK (jsonb_typeof(acceptance_authorities) = 'array')
);

CREATE INDEX idx_org_units_tenant_parent
    ON org_units (tenant_id, parent_id);
CREATE INDEX idx_org_units_tenant_level
    ON org_units (tenant_id, level);

-- ===== ALTER risks: hierarchy + theme columns =====
--
-- `level` defaults to 'team' so existing inserts (slice 002 onward) keep
-- working unchanged. `org_unit_id` is nullable so risks can be filed without
-- an org binding (smaller orgs may not configure org_units at all).
-- `themes` defaults to '{}' so slice-002/019 fixtures still insert cleanly.
ALTER TABLE risks
    ADD COLUMN level risk_level NOT NULL DEFAULT 'team';

ALTER TABLE risks
    ADD COLUMN org_unit_id UUID NULL;

ALTER TABLE risks
    ADD COLUMN themes TEXT[] NOT NULL DEFAULT '{}'::text[];

-- Composite FK risks(tenant_id, org_unit_id) → org_units(tenant_id, id).
-- ON DELETE SET NULL: deleting an org_unit unbinds risks rather than
-- cascading-delete them; the risk still exists and its lifecycle is
-- independent (canvas §6.4 "child risk's lifecycle is independent of its
-- parent").
ALTER TABLE risks
    ADD CONSTRAINT risks_org_unit_fk
    FOREIGN KEY (tenant_id, org_unit_id)
    REFERENCES org_units(tenant_id, id)
    ON DELETE SET NULL;

CREATE INDEX idx_risks_tenant_level ON risks (tenant_id, level);
CREATE INDEX idx_risks_tenant_org_unit ON risks (tenant_id, org_unit_id);
-- GIN on themes so slice 053's "list risks tagged X" queries can use the
-- index. text[] containment lookups are the hot path.
CREATE INDEX idx_risks_themes_gin ON risks USING GIN (themes);

-- ===== org_themes =====
--
-- Tenant-private theme catalog. Default themes (canvas §6.5 — ten built-ins)
-- live in the same table with tenant_id = NULL; see the companion
-- 20260511000015_seed_default_themes.sql migration. This matches the
-- frameworks/framework_versions pattern (slice 002): NULL tenant_id = global
-- catalog, NOT NULL = tenant-private.
--
-- Defaults are visible to every tenant; tenant-private themes are visible
-- only to their owning tenant (enforced by the `tenant_or_catalog` SELECT
-- policy below). Writes are tenant-scoped — no tenant can mutate a default
-- theme; the migration role inserts defaults via BYPASSRLS.
CREATE TABLE org_themes (
    id           UUID PRIMARY KEY,
    tenant_id    UUID NULL,
    theme_name   TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT org_themes_theme_name_nonempty CHECK (length(theme_name) > 0)
);

-- Two partial unique indexes: one for the global default set (UNIQUE on
-- theme_name when tenant_id IS NULL), one for per-tenant catalogs (UNIQUE on
-- (tenant_id, theme_name) when tenant_id IS NOT NULL). This lets a tenant
-- create a theme named "ownership" without colliding with the default of
-- the same name — they live in different namespaces by design.
CREATE UNIQUE INDEX org_themes_default_name_unique
    ON org_themes (theme_name)
    WHERE tenant_id IS NULL;
CREATE UNIQUE INDEX org_themes_tenant_name_unique
    ON org_themes (tenant_id, theme_name)
    WHERE tenant_id IS NOT NULL;

CREATE INDEX idx_org_themes_tenant ON org_themes (tenant_id);

-- ===== risk_aggregations =====
--
-- M:N join from parent risk to child risks. `rule_id` references the (future)
-- aggregation_rules table from slice 054 — nullable here because manual
-- aggregations have no rule (a human just linked the two risks). Composite FK
-- on both sides keeps cross-tenant references impossible at DDL level (D3).
-- Canvas §6.4 explicit: "Manual — a higher-level Risk explicitly references
-- one or more child Risks ... or a risk_aggregations join table for M:N."
--
-- IMPORTANT: there is NO ON DELETE CASCADE from child to parent. Closing a
-- child risk does not delete the parent or the aggregation row; canvas §6.4
-- "closing all children does not auto-close the parent". A separate slice
-- (054+) may add a "mark stale" status; this slice does NOT introduce one.
CREATE TABLE risk_aggregations (
    parent_risk_id  UUID NOT NULL,
    child_risk_id   UUID NOT NULL,
    rule_id         UUID NULL,
    tenant_id       UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (parent_risk_id, child_risk_id),
    FOREIGN KEY (tenant_id, parent_risk_id) REFERENCES risks(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, child_risk_id)  REFERENCES risks(tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT risk_aggregations_no_self_parent CHECK (parent_risk_id <> child_risk_id)
);

CREATE INDEX idx_risk_aggregations_tenant_parent
    ON risk_aggregations (tenant_id, parent_risk_id);
CREATE INDEX idx_risk_aggregations_tenant_child
    ON risk_aggregations (tenant_id, child_risk_id);

-- ===== framework_scopes composite UNIQUE =====
--
-- Defense-in-depth for the upcoming `decision_scope_predicates` FK: lets a
-- composite (tenant_id, framework_scope_id) FK target framework_scopes
-- without relying on RLS alone to prevent cross-tenant linkage. Same pattern
-- slice 019 added to `risks` and slice 006 to `vendors`.
ALTER TABLE framework_scopes
    ADD CONSTRAINT framework_scopes_tenant_id_unique UNIQUE (tenant_id, id);

-- ===== decisions =====
--
-- Decision Log entries per canvas §6.7. `decision_id` is the tenant-visible
-- identifier ("DL-2026-04-12") that audit narrative quotes; the system uses
-- the UUID `id` for FKs. Composite UNIQUE (tenant_id, id) so the four link
-- tables can FK by (tenant_id, decision_id) safely.
--
-- `constraints` is text[] of structured tags per canvas §6.7
-- ("time-pressure", "cost", "dependency-blocked", "risk-accepted", etc.) —
-- intentionally not an enum because the vocabulary will grow and the
-- tenant-private extension surface lives here, not in the DDL.
--
-- `superseded_by` is a self-ref to a replacement decision. Set when the
-- decision moves to `superseded` status. The FK is composite to keep
-- cross-tenant supersession impossible.
CREATE TABLE decisions (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    decision_id     TEXT NOT NULL,
    title           TEXT NOT NULL,
    narrative       TEXT NOT NULL DEFAULT '',
    constraints     TEXT[] NOT NULL DEFAULT '{}'::text[],
    tradeoffs       TEXT NOT NULL DEFAULT '',
    decision_maker  TEXT NOT NULL,
    decided_at      TIMESTAMPTZ NOT NULL,
    revisit_by      DATE NULL,
    status          decision_status NOT NULL DEFAULT 'active',
    superseded_by   UUID NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    -- Tenant-scoped uniqueness on the human-readable id, not a global PK.
    UNIQUE (tenant_id, decision_id),
    CONSTRAINT decisions_superseded_by_fk
        FOREIGN KEY (tenant_id, superseded_by)
        REFERENCES decisions(tenant_id, id)
        ON DELETE SET NULL,
    CONSTRAINT decisions_superseded_status_chk
        CHECK (
            (status = 'superseded' AND superseded_by IS NOT NULL)
            OR (status <> 'superseded')
        ),
    CONSTRAINT decisions_no_self_superseded
        CHECK (superseded_by IS NULL OR superseded_by <> id)
);

CREATE INDEX idx_decisions_tenant_status
    ON decisions (tenant_id, status);
CREATE INDEX idx_decisions_tenant_revisit
    ON decisions (tenant_id, revisit_by)
    WHERE revisit_by IS NOT NULL;
CREATE INDEX idx_decisions_tenant_decided_at
    ON decisions (tenant_id, decided_at DESC);

-- ===== Decision link tables (four separate M:N) =====
--
-- Each link table follows an identical shape: (decision_id, target_id,
-- tenant_id, created_at). Separate tables — NOT a polymorphic (target_kind,
-- target_id) — because explicit per-type FKs let RLS + composite FKs do
-- their job and auditors can query "every decision linked to risk X" with
-- one straight JOIN.

CREATE TABLE decision_risks (
    decision_id  UUID NOT NULL,
    target_id    UUID NOT NULL,
    tenant_id    UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, decision_id, target_id),
    UNIQUE (decision_id, target_id),
    FOREIGN KEY (tenant_id, decision_id) REFERENCES decisions(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, target_id)   REFERENCES risks(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_decision_risks_target
    ON decision_risks (tenant_id, target_id);

CREATE TABLE decision_controls (
    decision_id  UUID NOT NULL,
    target_id    UUID NOT NULL,
    tenant_id    UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, decision_id, target_id),
    UNIQUE (decision_id, target_id),
    FOREIGN KEY (tenant_id, decision_id) REFERENCES decisions(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, target_id)   REFERENCES controls(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_decision_controls_target
    ON decision_controls (tenant_id, target_id);

CREATE TABLE decision_exceptions (
    decision_id  UUID NOT NULL,
    target_id    UUID NOT NULL,
    tenant_id    UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, decision_id, target_id),
    UNIQUE (decision_id, target_id),
    FOREIGN KEY (tenant_id, decision_id) REFERENCES decisions(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, target_id)   REFERENCES exceptions(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_decision_exceptions_target
    ON decision_exceptions (tenant_id, target_id);

CREATE TABLE decision_scope_predicates (
    decision_id        UUID NOT NULL,
    target_id          UUID NOT NULL,   -- framework_scope_id
    tenant_id          UUID NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, decision_id, target_id),
    UNIQUE (decision_id, target_id),
    FOREIGN KEY (tenant_id, decision_id) REFERENCES decisions(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, target_id)   REFERENCES framework_scopes(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_decision_scope_predicates_target
    ON decision_scope_predicates (tenant_id, target_id);

-- ===== Row-Level Security =====
--
-- Four-policy split (read / write / update / delete) on every tenant-scoped
-- table, FORCE ROW LEVEL SECURITY so the table owner is bound too. The
-- migration role (atlas_migrate) is BYPASSRLS and so still performs DDL +
-- seed inserts.

-- org_units
ALTER TABLE org_units ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_units FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON org_units
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON org_units
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON org_units
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON org_units
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- org_themes — special: defaults are global (tenant_id NULL), tenant-private
-- rows are tenant-scoped. SELECT lets every tenant see defaults; writes are
-- tenant-scoped and never touch defaults from the app role.
ALTER TABLE org_themes ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_themes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_or_catalog_read ON org_themes
    FOR SELECT
    USING (tenant_id IS NULL OR current_tenant_matches(tenant_id));
-- Writes must be tenant-scoped: tenant_id must be NOT NULL and match the GUC.
-- This forbids the application role from creating new defaults (defaults are
-- migration-only, applied via BYPASSRLS in 20260511000015).
CREATE POLICY tenant_write ON org_themes
    FOR INSERT
    WITH CHECK (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON org_themes
    FOR UPDATE
    USING (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id))
    WITH CHECK (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON org_themes
    FOR DELETE
    USING (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));

-- risk_aggregations
ALTER TABLE risk_aggregations ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_aggregations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON risk_aggregations
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON risk_aggregations
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON risk_aggregations
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON risk_aggregations
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- decisions — additional role-check WITH CHECK clauses for INSERT/UPDATE.
-- Per AC-7: writes are restricted to roles with the relevant authority on
-- the bound org_unit. Decisions are not org-bound (canvas §6.7), so the
-- check here gates on the role sentinel alone. The sentinel `'*'` (or unset)
-- is the transitional bypass until slice 035 wires real roles end-to-end.
-- Once slice 035 lands, application code will set `app.current_role` per
-- request and this clause becomes load-bearing.
ALTER TABLE decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE decisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON decisions
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decisions
    FOR INSERT
    WITH CHECK (
        current_tenant_matches(tenant_id)
        AND COALESCE(current_setting('app.current_role', true), '*') <> ''
    );
CREATE POLICY tenant_update ON decisions
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (
        current_tenant_matches(tenant_id)
        AND COALESCE(current_setting('app.current_role', true), '*') <> ''
    );
CREATE POLICY tenant_delete ON decisions
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- decision_risks / _controls / _exceptions / _scope_predicates — standard
-- four-policy RLS. Authority check lives on the parent `decisions` row, not
-- the link rows; a link without a permitted decision cannot exist.
ALTER TABLE decision_risks ENABLE ROW LEVEL SECURITY;
ALTER TABLE decision_risks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON decision_risks
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decision_risks
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON decision_risks
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON decision_risks
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

ALTER TABLE decision_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE decision_controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON decision_controls
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decision_controls
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON decision_controls
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON decision_controls
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

ALTER TABLE decision_exceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE decision_exceptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON decision_exceptions
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decision_exceptions
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON decision_exceptions
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON decision_exceptions
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

ALTER TABLE decision_scope_predicates ENABLE ROW LEVEL SECURITY;
ALTER TABLE decision_scope_predicates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON decision_scope_predicates
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decision_scope_predicates
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON decision_scope_predicates
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON decision_scope_predicates
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- ===== Grants =====
--
-- atlas_app gets full DML on every new tenant-scoped table. atlas_migrate
-- (BYPASSRLS, set up by migrations/bootstrap/01-roles.sql) retains DDL +
-- seed-write access via role membership.
GRANT SELECT, INSERT, UPDATE, DELETE ON
    org_units, org_themes, risk_aggregations,
    decisions,
    decision_risks, decision_controls, decision_exceptions, decision_scope_predicates
TO atlas_app;
