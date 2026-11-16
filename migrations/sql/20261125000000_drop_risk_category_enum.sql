-- security-atlas — #1819: drop the risk_category enum (contract step of #1745).
--
-- #1809 added `risks.category_id`, a reference to the tenant's own
-- organisation-owned category, and kept the old `risks.category` enum
-- dual-written through the `risks_category_sync` trigger. Every writer now
-- sends the reference, so this migration retires the enum column, its
-- trigger and the type. The reference is the only source of truth from here.
--
-- Guard: the drop refuses to run while any risk's reference disagrees with
-- its enum value: a null reference, a reference to no category, or one whose
-- slug is not the enum value. The expand step's trigger should have made
-- that impossible, but a live install must never lose a category silently,
-- so the migration checks rather than trusts. It runs as atlas_migrate
-- (BYPASSRLS), so the check sees every tenant's rows, and it locks `risks`
-- first so no write can slip in between the check and the drop.
--
-- The lock, the check and the drops are one DO statement, so they are
-- atomic whether the runner wraps the file in a transaction (migrate.sh's
-- --single-transaction) or applies it in autocommit (plain `psql -f`, as
-- some CI jobs do), where a bare LOCK TABLE is an error.

DO $$
DECLARE
    v_bad   BIGINT;
    v_first TEXT;
BEGIN
    LOCK TABLE risks IN ACCESS EXCLUSIVE MODE;

    -- Re-applied after a successful run: nothing left to check or drop.
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'risks' AND column_name = 'category'
    ) THEN
        RETURN;
    END IF;

    -- The guard is only as good as what it can see. Under a role that RLS
    -- binds, the count below would see no tenant's rows and pass vacuously,
    -- so refuse to run under anything but a superuser or BYPASSRLS role.
    IF NOT (SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user) THEN
        RAISE EXCEPTION
            'refusing to drop risks.category: role % cannot see every tenant''s risks (needs BYPASSRLS)',
            current_user
            USING ERRCODE = 'insufficient_privilege',
                  HINT = 'Run the migration as atlas_migrate.';
    END IF;

    SELECT count(*), min(r.id::text)
    INTO v_bad, v_first
    FROM risks r
    LEFT JOIN org_risk_categories c
        ON c.tenant_id = r.tenant_id AND c.id = r.category_id
    WHERE r.category_id IS NULL
       OR c.id IS NULL
       OR c.slug <> r.category::text;

    IF v_bad > 0 THEN
        RAISE EXCEPTION
            'refusing to drop risks.category: % risk(s) have a category reference that does not match their risk_category value (first: %)',
            v_bad, v_first
            USING ERRCODE = 'check_violation',
                  HINT = 'Set each listed risk''s category_id to the tenant category whose slug equals its category, then re-run.';
    END IF;

    DROP TRIGGER IF EXISTS risks_category_sync_trg ON risks;
    DROP FUNCTION IF EXISTS risks_category_sync();
    ALTER TABLE risks DROP COLUMN category;
    DROP TYPE IF EXISTS risk_category;
END $$;

-- No-ops at runtime (the DO block above already dropped these). They stay
-- as top-level statements because sqlc reads the schema from these files
-- and cannot see DDL inside a DO block.
DROP TRIGGER IF EXISTS risks_category_sync_trg ON risks;
DROP FUNCTION IF EXISTS risks_category_sync();

ALTER TABLE risks DROP COLUMN IF EXISTS category;

DROP TYPE IF EXISTS risk_category;

-- The dropped trigger let a SQL writer name a category by slug alone and
-- seeded a bare-UUID tenant's categories on the way (the demo seed, SQL
-- fixtures). This keeps that, explicitly: risk_category_id(tenant, slug)
-- seeds the tenant's categories if it has none and returns the id of the
-- one with that slug, or raises if there is none. SECURITY INVOKER, so
-- under atlas_app RLS still limits it to the caller's own tenant.
CREATE OR REPLACE FUNCTION risk_category_id(p_tenant_id UUID, p_slug TEXT)
RETURNS UUID
LANGUAGE plpgsql
AS $$
DECLARE
    v_id UUID;
BEGIN
    PERFORM seed_risk_categories(p_tenant_id);
    SELECT id INTO v_id
    FROM org_risk_categories
    WHERE tenant_id = p_tenant_id AND slug = p_slug;
    IF v_id IS NULL THEN
        RAISE EXCEPTION 'risk category % not found for tenant %', p_slug, p_tenant_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN v_id;
END;
$$;

GRANT EXECUTE ON FUNCTION risk_category_id(UUID, TEXT) TO atlas_app;
