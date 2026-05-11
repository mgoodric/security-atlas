-- security-atlas — role bootstrap. Run ONCE per database before applying
-- versioned migrations.
--
-- Two roles:
--   atlas_migrate — used by Atlas for DDL. BYPASSRLS so it can apply schema
--                   changes against tables that have FORCE ROW LEVEL SECURITY.
--                   No application code path should ever connect as this role.
--   atlas_app     — used by application + integration tests. NOSUPERUSER and
--                   NOBYPASSRLS so the RLS policies are actually enforced.
--
-- Passwords are placeholders for local dev. Production deployments set them
-- via environment-injected SQL (do NOT commit production secrets).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_migrate') THEN
        CREATE ROLE atlas_migrate LOGIN PASSWORD 'atlas_migrate_local' BYPASSRLS;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_app') THEN
        CREATE ROLE atlas_app LOGIN PASSWORD 'atlas_app_local' NOSUPERUSER NOBYPASSRLS;
    END IF;
END $$;

-- Allow atlas_migrate to own DDL and grant rights downstream. GRANT on a
-- DATABASE needs a literal name; use dynamic SQL so this script works in any
-- target database.
DO $$
BEGIN
    EXECUTE format('GRANT ALL PRIVILEGES ON DATABASE %I TO atlas_migrate', current_database());
END $$;

GRANT USAGE ON SCHEMA public TO atlas_app;
