-- security-atlas — #1809: organisation-owned risk categories (expand step).
--
-- Parent spec #1745 rolls categories out as expand -> migrate -> contract.
-- This migration is the expand step:
--
--   1. `org_risk_categories`: a tenant-scoped table, four-policy RLS. Slug is
--      unique per tenant and immutable; display name is unique among a
--      tenant's active (non-archived) categories; exactly one default per
--      tenant (partial unique index).
--   2. `seed_risk_categories(tenant)`: idempotently gives a tenant the seven
--      categories whose slugs equal today's `risk_category` enum values, with
--      `operational` as the default. Called here for existing tenants, at
--      tenant creation (admin tenant create, first-user bootstrap, the
--      self-host seed), and lazily by the store and the trigger below for a
--      bare-UUID tenant that has no `tenants` row.
--   3. `risks.category_id`: a reference to the tenant's own category,
--      backfilled from the enum, NOT NULL after the backfill.
--   4. `risks_category_sync`: keeps the reference and the enum in agreement
--      on every write path. A writer that sends the enum only (the MCP
--      applier, aggregation meta-risks, demo seed, SQL fixtures) gets the
--      matching reference; a writer that sends the reference gets the enum
--      from its slug; a writer that sends both must send a matching pair.
--
-- Nothing user-visible changes. The contract step drops the enum column,
-- the enum type and the trigger.

CREATE TABLE org_risk_categories (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    slug         TEXT NOT NULL,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    archived     BOOLEAN NOT NULL DEFAULT false,
    is_default   BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT org_risk_categories_slug_shape
        CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,62}$'),
    CONSTRAINT org_risk_categories_name_nonempty
        CHECK (length(btrim(name)) > 0 AND length(name) <= 200),
    CONSTRAINT org_risk_categories_description_len
        CHECK (length(description) <= 2000),
    -- The default is what aggregation meta-risks fall back to, so it cannot
    -- be hidden from pickers.
    CONSTRAINT org_risk_categories_default_active
        CHECK (NOT (is_default AND archived)),
    -- Target of the composite FK from risks: a risk can only reference a
    -- category of its own tenant.
    CONSTRAINT org_risk_categories_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT org_risk_categories_tenant_slug_unique UNIQUE (tenant_id, slug)
);

CREATE UNIQUE INDEX org_risk_categories_active_name_unique
    ON org_risk_categories (tenant_id, lower(name))
    WHERE NOT archived;

CREATE UNIQUE INDEX org_risk_categories_one_default
    ON org_risk_categories (tenant_id)
    WHERE is_default;

-- Slugs never change: API clients and imports key on them.
CREATE OR REPLACE FUNCTION org_risk_categories_slug_immutable()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.slug IS DISTINCT FROM OLD.slug THEN
        RAISE EXCEPTION 'org_risk_categories.slug is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id THEN
        RAISE EXCEPTION 'org_risk_categories.tenant_id is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER org_risk_categories_slug_immutable_trg
    BEFORE UPDATE ON org_risk_categories
    FOR EACH ROW
    EXECUTE FUNCTION org_risk_categories_slug_immutable();

ALTER TABLE org_risk_categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_risk_categories FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON org_risk_categories
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON org_risk_categories
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON org_risk_categories
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON org_risk_categories
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON org_risk_categories TO atlas_app;

-- Idempotent seed. SECURITY INVOKER: under atlas_app the RLS write policy
-- still binds, so a caller can only seed its own tenant. ON CONFLICT DO
-- NOTHING (no target) skips a slug the tenant already has, and also any
-- seed row that would collide with a category the tenant has since renamed
-- onto a seeded name. The default goes to `operational` only when the
-- tenant has no default yet.
CREATE OR REPLACE FUNCTION seed_risk_categories(p_tenant_id UUID)
RETURNS VOID
LANGUAGE sql
AS $$
    INSERT INTO org_risk_categories (tenant_id, slug, name, description, is_default)
    SELECT
        p_tenant_id,
        s.slug,
        s.name,
        s.description,
        s.slug = 'operational' AND NOT EXISTS (
            SELECT 1 FROM org_risk_categories d
            WHERE d.tenant_id = p_tenant_id AND d.is_default
        )
    FROM (VALUES
        ('confidentiality', 'Confidentiality',
            'Unauthorised disclosure of information.'),
        ('integrity', 'Integrity',
            'Unauthorised or accidental modification of information or systems.'),
        ('availability', 'Availability',
            'Loss of access to information or services.'),
        ('privacy', 'Privacy',
            'Handling of personal data contrary to obligations or expectations.'),
        ('regulatory', 'Regulatory',
            'Failure to meet a legal, regulatory or contractual requirement.'),
        ('operational', 'Operational',
            'Failure of people, process or technology in day-to-day operation.'),
        ('financial', 'Financial',
            'Direct monetary loss.')
    ) AS s(slug, name, description)
    ON CONFLICT DO NOTHING;
$$;

GRANT EXECUTE ON FUNCTION seed_risk_categories(UUID) TO atlas_app;

-- Existing tenants: every `tenants` row, plus any bare-UUID tenant that only
-- appears on risks (tenants predating slice 144).
SELECT seed_risk_categories(t.id)
FROM (
    SELECT id FROM tenants
    UNION
    SELECT DISTINCT tenant_id FROM risks
) t;

-- ===== risks.category_id =====

ALTER TABLE risks ADD COLUMN category_id UUID NULL;

UPDATE risks r
SET category_id = c.id
FROM org_risk_categories c
WHERE c.tenant_id = r.tenant_id
  AND c.slug = r.category::text
  AND r.category_id IS NULL;

ALTER TABLE risks ALTER COLUMN category_id SET NOT NULL;

ALTER TABLE risks
    ADD CONSTRAINT risks_category_fk
    FOREIGN KEY (tenant_id, category_id)
    REFERENCES org_risk_categories (tenant_id, id);

CREATE INDEX idx_risks_category_id ON risks (tenant_id, category_id);

-- Dual-write: keep `category` (enum) and `category_id` in agreement.
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
            -- Only the reference moved: carry the enum along. The cast fails
            -- for a slug outside the enum, which only exists after the
            -- management API lands with the contract step.
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

CREATE TRIGGER risks_category_sync_trg
    BEFORE INSERT OR UPDATE OF category, category_id ON risks
    FOR EACH ROW
    EXECUTE FUNCTION risks_category_sync();
