-- Reverse #1819: bring back the risk_category enum, the risks.category
-- column and the risks_category_sync dual-write trigger.
--
-- Not lossless. The enum holds only the seven built-in slugs; a risk on an
-- organisation category added after the contract step (any other slug) gets
-- `operational`, and its category_id is left alone, so the reference stays
-- the truth and the enum value for those risks is a placeholder. See
-- docs/audit-log/1819-drop-risk-category-enum-decisions.md.

DROP FUNCTION IF EXISTS risk_category_id(UUID, TEXT);

DO $$ BEGIN
    CREATE TYPE risk_category AS ENUM (
        'confidentiality',
        'integrity',
        'availability',
        'privacy',
        'regulatory',
        'operational',
        'financial'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE risks ADD COLUMN IF NOT EXISTS category risk_category NULL;

UPDATE risks r
SET category = CASE
        WHEN c.slug IN ('confidentiality', 'integrity', 'availability',
                        'privacy', 'regulatory', 'operational', 'financial')
            THEN c.slug::risk_category
        ELSE 'operational'::risk_category
    END
FROM org_risk_categories c
WHERE c.tenant_id = r.tenant_id
  AND c.id = r.category_id
  AND r.category IS NULL;

ALTER TABLE risks ALTER COLUMN category SET NOT NULL;

-- The #1809 dual-write trigger, as it was.
CREATE OR REPLACE FUNCTION risks_category_sync()
RETURNS TRIGGER AS $$
DECLARE
    v_slug TEXT;
BEGIN
    IF NEW.category_id IS NOT NULL
       AND (TG_OP = 'INSERT' OR NEW.category_id IS DISTINCT FROM OLD.category_id) THEN
        SELECT slug INTO v_slug
        FROM org_risk_categories
        WHERE tenant_id = NEW.tenant_id AND id = NEW.category_id;
        IF v_slug IS NULL THEN
            RAISE EXCEPTION 'risk category % not found for tenant', NEW.category_id
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        IF TG_OP = 'UPDATE' AND NEW.category IS NOT DISTINCT FROM OLD.category THEN
            NEW.category := v_slug::risk_category;
        ELSIF NEW.category::text <> v_slug THEN
            RAISE EXCEPTION 'risk category % (slug %) disagrees with category %',
                NEW.category_id, v_slug, NEW.category
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_OP = 'INSERT' OR NEW.category IS DISTINCT FROM OLD.category THEN
        PERFORM seed_risk_categories(NEW.tenant_id);
        SELECT id INTO NEW.category_id
        FROM org_risk_categories
        WHERE tenant_id = NEW.tenant_id AND slug = NEW.category::text;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS risks_category_sync_trg ON risks;
CREATE TRIGGER risks_category_sync_trg
    BEFORE INSERT OR UPDATE OF category, category_id ON risks
    FOR EACH ROW
    EXECUTE FUNCTION risks_category_sync();
