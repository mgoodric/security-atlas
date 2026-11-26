-- security-atlas — #1765: demo seed into the sole tenant.
--
-- On a single-tenant install, `atlas-cli demo seed` writes the demo
-- dataset into the existing (bootstrap) tenant instead of creating a
-- second tenant. That tenant holds real rows too (the stock bootstrap
-- seed), so `demo teardown` cannot delete by tenant_id. Instead the seed
-- records here every row it added, and teardown deletes exactly those.
--
--   table_name  the tenant-scoped table the row lives in
--   row_key     the row's primary-key columns as a JSON object (every
--               column when the table has no primary key); teardown
--               matches it with to_jsonb(row) @> row_key
--
-- A tenant with rows here was demo-seeded in place; teardown never drops
-- such a tenant. Tenant-scoped and RLS-enforced (invariant #6); the
-- seeder writes through the BYPASSRLS migrate pool, like the rest of the
-- demo dataset.

CREATE TABLE IF NOT EXISTS demo_seed_rows (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    table_name  TEXT NOT NULL,
    row_key     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT demo_seed_rows_unique UNIQUE (tenant_id, table_name, row_key)
);

ALTER TABLE demo_seed_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE demo_seed_rows FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    BEGIN
        CREATE POLICY tenant_read ON demo_seed_rows FOR SELECT USING (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_write ON demo_seed_rows FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_update ON demo_seed_rows FOR UPDATE USING (current_tenant_matches(tenant_id)) WITH CHECK (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_delete ON demo_seed_rows FOR DELETE USING (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
END $$;

GRANT SELECT ON demo_seed_rows TO atlas_app;
