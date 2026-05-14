-- security-atlas — vendor lite module (slice 024).
--
-- "Lite" means the minimum to retire the user's vendor-tracking spreadsheet.
-- Sized for ~30–80 vendors at a security-product startup (canvas §1.4 +
-- §10.1). Phase 2 adds questionnaire issuance and trust-center scraping;
-- both are explicit anti-criteria here.
--
-- Two tables land in this slice:
--
--   vendors            — the lite TPRM entity. Contract dates, DPA status,
--                        review cadence, criticality, last-review-date,
--                        owner, optional linked SOW URI. tenant-scoped, RLS.
--
--   vendor_scope_cells — join from vendors to slice-017 scope_cells. A
--                        vendor relationship can span multiple cells (e.g.,
--                        the same datadog org may serve prod-EU and prod-US
--                        but not staging). canvas Invariant 4 = scope is a
--                        tuple set, not a tree, so this is a many-to-many.
--
-- The "overdue" calculation (AC-4) is a derived field, not stored. The Go
-- store computes it from last_review_date + cadence vs now() so a missed-
-- review streak does not require a backfill migration whenever cadence
-- semantics change.
--
-- RLS pattern mirrors slice 014's evidence_kind_schemas (tenant_write,
-- tenant_update, tenant_delete with explicit WITH CHECK) so cross-tenant
-- writes are denied at the database, not the application. Vendors have no
-- "global / catalog" rows in v1 — every vendor belongs to exactly one
-- tenant.

-- ===== Enum types =====
--
-- criticality is the slice-024 scoring band. Three buckets is enough for a
-- 30–80-vendor portfolio; FAIR or 5-band scoring is phase-2 territory.
--
-- Wrapped in a DO/EXCEPTION block for re-run idempotency (slice 065 bug #3):
-- Postgres has no `CREATE TYPE IF NOT EXISTS`, and the self-host bootstrap
-- re-applies every migration on each `docker compose up`.

DO $$ BEGIN
    CREATE TYPE vendor_criticality AS ENUM (
        'low',
        'medium',
        'high'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- review_cadence captures the interval between reviews. Stored as an enum so
-- the overdue-computation can map cadence -> interval in one place. Values
-- intentionally match security-program-typical cadences (no "decadal").

DO $$ BEGIN
    CREATE TYPE vendor_review_cadence AS ENUM (
        'monthly',
        'quarterly',
        'biannual',
        'annual'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== vendors =====
--
-- One row per third party the tenant tracks. domain is the natural key when
-- present (e.g., "datadoghq.com") but is optional — some vendors don't have
-- a public domain or the tenant tracks an internal-only relationship. A
-- partial unique index handles the natural-key dedup without tripping the
-- NULLs-distinct default on UNIQUE (memory rule: feedback_postgres_constraints.md).

CREATE TABLE vendors (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    name                TEXT NOT NULL,
    domain              TEXT NULL,
    criticality         vendor_criticality NOT NULL DEFAULT 'medium',
    contract_start      DATE NULL,
    contract_end        DATE NULL,
    dpa_signed          BOOLEAN NOT NULL DEFAULT FALSE,
    dpa_signed_at       DATE NULL,
    review_cadence      vendor_review_cadence NOT NULL DEFAULT 'annual',
    last_review_date    DATE NULL,
    owner_user          TEXT NOT NULL DEFAULT '',
    linked_sow_uri      TEXT NULL,
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Name must be non-empty so the list view always has something to render.
    CONSTRAINT vendors_name_nonempty CHECK (length(name) > 0),

    -- If dpa_signed=true, dpa_signed_at must be present. The schema can't
    -- mandate the converse (dpa_signed_at set => dpa_signed=true), but the
    -- application enforces it.
    CONSTRAINT vendors_dpa_consistent
        CHECK (dpa_signed = FALSE OR dpa_signed_at IS NOT NULL),

    -- contract_end >= contract_start when both are set. Helps the upcoming
    -- contract-renewal dashboard panel not have to defend against junk dates.
    CONSTRAINT vendors_contract_range
        CHECK (contract_end IS NULL OR contract_start IS NULL OR contract_end >= contract_start)
);

-- Composite uniqueness across (tenant_id, id) supports cross-tenant-safe FK
-- targets if a future slice needs to link evidence to vendors. Mirrors the
-- slice-002 controls pattern.
ALTER TABLE vendors
    ADD CONSTRAINT vendors_tenant_id_unique UNIQUE (tenant_id, id);

-- Partial unique on (tenant_id, lower(domain)) — case-insensitive natural key,
-- only enforced when domain is non-NULL. NULLs-distinct gotcha avoided by
-- the WHERE clause: rows without a domain never collide.
CREATE UNIQUE INDEX vendors_tenant_domain_uniq
    ON vendors (tenant_id, lower(domain))
    WHERE domain IS NOT NULL;

-- Hot-path indexes for the list view filters (AC-2) and the burndown
-- aggregate (AC-3).
CREATE INDEX idx_vendors_tenant_criticality
    ON vendors (tenant_id, criticality);

CREATE INDEX idx_vendors_tenant_last_review
    ON vendors (tenant_id, last_review_date)
    WHERE last_review_date IS NOT NULL;

-- ===== vendor_scope_cells =====
--
-- Many-to-many join from vendors to slice-017 scope_cells. Composite FK
-- (tenant_id, vendor_id) -> vendors(tenant_id, id) keeps the cross-tenant
-- door shut at the DB layer. No FK to scope_cells(id) — slice 017 keeps
-- those in scope_cells without a tenant-composite key, so we rely on the
-- tenant_id column + RLS to keep the cell reference honest.

CREATE TABLE vendor_scope_cells (
    tenant_id           UUID NOT NULL,
    vendor_id           UUID NOT NULL,
    scope_cell_id       UUID NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, vendor_id, scope_cell_id),
    FOREIGN KEY (tenant_id, vendor_id) REFERENCES vendors(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_vendor_scope_cells_cell
    ON vendor_scope_cells (tenant_id, scope_cell_id);

-- ===== Row-Level Security =====
--
-- Mirrors slice 014's tenant_write/read/update/delete split for explicit
-- WITH CHECK. atlas_migrate has BYPASSRLS so DDL applies under FORCE.

ALTER TABLE vendors ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON vendors
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON vendors
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON vendors
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON vendors
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

ALTER TABLE vendor_scope_cells ENABLE ROW LEVEL SECURITY;
ALTER TABLE vendor_scope_cells FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON vendor_scope_cells
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON vendor_scope_cells
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON vendor_scope_cells
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON vendor_scope_cells
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON
    vendors, vendor_scope_cells
TO atlas_app;
