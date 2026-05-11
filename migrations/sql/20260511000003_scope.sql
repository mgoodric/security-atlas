-- security-atlas — scope dimensions + scope cells (slice 017).
--
-- Adds the canonical, tenant-configurable multidimensional scope model on top of
-- the slice-002 baseline. The slice-002 `scopes` table stays as a denormalized
-- projection so the existing `evidence_records.scope_id` FK remains valid; the
-- new `scope_cells` table is the SOURCE OF TRUTH and supports tenant-defined
-- dimensions via a JSONB map.
--
-- Why two tables (scopes vs scope_cells):
--   - `scopes` (slice 002) has positional columns (bu / env / geo / cloud / dc /
--     product) and is referenced by `evidence_records.scope_id`. We do NOT drop
--     or rename it — too many existing FKs.
--   - `scope_cells` (this slice) holds the extensible JSONB tuple. The platform
--     mirrors writes into `scopes` with the same UUID so the FK from evidence
--     still resolves. The mirror is one-way (cells → scopes).
--
-- See ARCHITECTURE_CANVAS.md §2.4 + §5.1–5.3 and docs/issues/017-scope-dimensions-applicability.md.

-- ===== scope_dimensions =====
--
-- Per-tenant declaration of the dimension schema. The platform seeds the
-- builtin set (business_unit, environment, geography, cloud_account,
-- data_classification, product_line) on tenant bootstrap; admins can add
-- custom dimensions.
--
-- value_type is TEXT-typed (not an enum) so future dimension types (number,
-- bool) can land without a migration. v1 only supports 'string'.

CREATE TABLE scope_dimensions (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    name            TEXT NOT NULL,
    value_type      TEXT NOT NULL DEFAULT 'string',
    allowed_values  JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_required     BOOLEAN NOT NULL DEFAULT FALSE,
    is_builtin      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Per-tenant uniqueness on dimension name. Both columns are NOT NULL so the
    -- NULLS-DISTINCT-by-default semantics on UNIQUE do not bite us here.
    CONSTRAINT scope_dimensions_tenant_name_unique UNIQUE (tenant_id, name),
    CONSTRAINT scope_dimensions_value_type_chk CHECK (value_type IN ('string'))
);

-- ===== scope_cells =====
--
-- A scope cell is a tuple over the tenant's declared dimensions. dimensions is
-- a JSONB object (key = dimension name, value = string). dimensions_hash is the
-- SHA-256 of the canonical (key-sorted) JSON, written by the application; the
-- UNIQUE constraint over (tenant_id, dimensions_hash) prevents duplicate cells.
--
-- We do NOT use a JSONB expression index for uniqueness because Postgres does
-- not order object keys deterministically; the application-side canonical hash
-- is the simplest correct approach and stays out of the way of sqlc.

CREATE TABLE scope_cells (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    label               TEXT NOT NULL DEFAULT '',
    dimensions          JSONB NOT NULL,
    dimensions_hash     TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT scope_cells_tenant_hash_unique UNIQUE (tenant_id, dimensions_hash)
);

CREATE INDEX idx_scope_cells_tenant_created
    ON scope_cells (tenant_id, created_at DESC);

CREATE INDEX idx_scope_dimensions_tenant_builtin
    ON scope_dimensions (tenant_id, is_builtin);

-- ===== Row-Level Security =====
--
-- Tenant-scoped. Uses the helper from slice 002. atlas_migrate has BYPASSRLS so
-- this DDL applies even with FORCE ROW LEVEL SECURITY set.

ALTER TABLE scope_dimensions ENABLE ROW LEVEL SECURITY;
ALTER TABLE scope_dimensions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scope_dimensions
    USING (current_tenant_matches(tenant_id));

ALTER TABLE scope_cells ENABLE ROW LEVEL SECURITY;
ALTER TABLE scope_cells FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scope_cells
    USING (current_tenant_matches(tenant_id));

-- Grant DML to the application role (same pattern as slice 002).
GRANT SELECT, INSERT, UPDATE, DELETE ON
    scope_dimensions, scope_cells
TO atlas_app;
