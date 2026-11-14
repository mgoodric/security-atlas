-- Reverse #1809: drop the risk category reference and table. The enum column
-- stays the source of truth, so no risk loses its category.

DROP TRIGGER IF EXISTS risks_category_sync_trg ON risks;
DROP FUNCTION IF EXISTS risks_category_sync();

DROP INDEX IF EXISTS idx_risks_category_id;
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_category_fk;
ALTER TABLE risks DROP COLUMN IF EXISTS category_id;

DROP FUNCTION IF EXISTS seed_risk_categories(UUID);

DROP TRIGGER IF EXISTS org_risk_categories_slug_immutable_trg ON org_risk_categories;
DROP FUNCTION IF EXISTS org_risk_categories_slug_immutable();
DROP TABLE IF EXISTS org_risk_categories;
