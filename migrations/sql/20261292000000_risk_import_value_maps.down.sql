-- Reverses 20261292000000_risk_import_value_maps.sql. A row's category_slug
-- comes back as the slug its value maps to, or its value when unmapped.
ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS category_slug TEXT NULL;
UPDATE risk_import_rows r
SET category_slug = COALESCE(
    (SELECT m.target FROM risk_import_value_maps m
     WHERE m.batch_id = r.batch_id AND m.kind = 'category'
       AND lower(m.value) = lower(r.category_value)),
    r.category_value);
ALTER TABLE risk_import_rows
    ALTER COLUMN category_slug SET NOT NULL;
DROP TABLE IF EXISTS risk_import_value_maps;
ALTER TABLE risk_import_rows
    DROP COLUMN IF EXISTS theme_values,
    DROP COLUMN IF EXISTS category_value;
