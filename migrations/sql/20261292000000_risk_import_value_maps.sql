-- security-atlas — #2844: category and theme value mapping for the risk
-- register import (spec #2812, Q13).
--
-- Categories and themes are organisation-owned, so a register's spelling
-- variants ("Ops", "Operational") are mapped once per batch rather than
-- matched per row:
--
--   risk_import_rows.category_value  the row's category cell as written,
--                                    trimmed; replaces category_slug, the
--                                    tracer's per-row exact match;
--   risk_import_rows.theme_values    the row's theme cell split into its
--                                    values, trimmed;
--   risk_import_value_maps           one row per distinct value of a batch
--                                    (kind category or theme, values that
--                                    differ only in case are one value),
--                                    with the tenant category slug or theme
--                                    name it maps to, NULL while unmapped.
--
-- Approval reads the map: a row takes the mapped category and themes, and
-- a row with any unmapped value is rejected with a reason. Nothing here
-- creates a category or theme; that goes through the approver-gated,
-- audited creation calls (taxonomy_audit_log).
--
-- Backfill: a row staged before this migration carries the slug its value
-- matched, so that slug becomes its value and maps to itself.

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS category_value TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS theme_values   TEXT[] NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS risk_import_value_maps (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    batch_id    UUID NOT NULL,
    kind        TEXT NOT NULL,
    -- The value as first written in the file, trimmed.
    value       TEXT NOT NULL,
    -- The category slug or theme name it maps to; NULL is unmapped.
    target      TEXT NULL,
    -- Who mapped it, '' when it matched an existing category or theme at
    -- upload and nobody has changed it since.
    mapped_by   TEXT NOT NULL DEFAULT '',
    mapped_at   TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT risk_import_value_maps_kind_chk CHECK (kind IN ('category', 'theme')),
    CONSTRAINT risk_import_value_maps_value_chk CHECK (length(value) > 0),
    CONSTRAINT risk_import_value_maps_target_chk CHECK (target IS NULL OR length(target) > 0),
    CONSTRAINT risk_import_value_maps_batch_tenant_fk
        FOREIGN KEY (tenant_id, batch_id)
        REFERENCES risk_import_batches (tenant_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS risk_import_value_maps_batch_value_key
    ON risk_import_value_maps (batch_id, kind, value);
CREATE INDEX IF NOT EXISTS idx_risk_import_value_maps_tenant_batch
    ON risk_import_value_maps (tenant_id, batch_id);

ALTER TABLE risk_import_value_maps ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_import_value_maps FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    BEGIN
        CREATE POLICY tenant_read ON risk_import_value_maps
            FOR SELECT USING (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_write ON risk_import_value_maps
            FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_update ON risk_import_value_maps
            FOR UPDATE USING (current_tenant_matches(tenant_id))
            WITH CHECK (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
    BEGIN
        CREATE POLICY tenant_delete ON risk_import_value_maps
            FOR DELETE USING (current_tenant_matches(tenant_id));
    EXCEPTION WHEN duplicate_object THEN NULL; END;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON risk_import_value_maps TO atlas_app;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'risk_import_rows' AND column_name = 'category_slug') THEN
        UPDATE risk_import_rows SET category_value = category_slug WHERE category_value = '';
        INSERT INTO risk_import_value_maps (tenant_id, batch_id, kind, value, target)
        SELECT DISTINCT tenant_id, batch_id, 'category', category_slug, category_slug
        FROM risk_import_rows
        ON CONFLICT DO NOTHING;
        ALTER TABLE risk_import_rows DROP COLUMN category_slug;
    END IF;
END $$;
