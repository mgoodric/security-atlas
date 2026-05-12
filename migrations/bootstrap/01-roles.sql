-- security-atlas — role bootstrap. Run ONCE per database before applying
-- versioned migrations.
--
-- Three roles:
--   atlas_migrate          — used by Atlas for DDL. BYPASSRLS so it can apply
--                            schema changes against tables that have FORCE
--                            ROW LEVEL SECURITY. No application code path
--                            should ever connect as this role.
--   atlas_app              — used by application + integration tests.
--                            NOSUPERUSER and NOBYPASSRLS so the RLS policies
--                            are actually enforced.
--   atlas_service_account  — slice 033. BYPASSRLS, NOLOGIN, NOINHERIT.
--                            Reachable only via `SET LOCAL ROLE
--                            atlas_service_account` from a session
--                            authenticated as atlas_app. The canonical
--                            pattern for the rare cross-tenant read (e.g.
--                            a future platform-wide audit-log scan); see
--                            docs/architecture/rls.md. No production caller
--                            in v1; the role + GRANT chain exists so the
--                            shape is locked before any future feature is
--                            tempted to ALTER atlas_app BYPASSRLS.
--
-- Passwords are intentionally NOT set here. Set them out-of-band via:
--   ALTER ROLE atlas_app PASSWORD '<value-from-env>';
-- Local dev typically uses trust/peer auth on a loopback socket; CI sets
-- passwords from secrets after this script runs (see .github/workflows/ci.yml).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_migrate') THEN
        CREATE ROLE atlas_migrate LOGIN BYPASSRLS;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_app') THEN
        CREATE ROLE atlas_app LOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;

    -- Slice 033: cross-tenant-read role. BYPASSRLS so it can read across
    -- tenants when a `SET LOCAL ROLE` switch is in effect. NOLOGIN so it
    -- cannot establish a session from outside the server — the only way
    -- to reach it is `SET LOCAL ROLE atlas_service_account` from an
    -- atlas_app session, which means the application code path is the
    -- only path. NOINHERIT keeps the privilege explicit per transaction;
    -- a stray atlas_app session does NOT inherit BYPASSRLS by default.
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_service_account') THEN
        CREATE ROLE atlas_service_account NOLOGIN NOINHERIT BYPASSRLS;
    END IF;

    -- Allow atlas_app to switch into the service-account role for the
    -- duration of a single transaction via `SET LOCAL ROLE`. Without this
    -- GRANT the switch fails with "permission denied to set role".
    -- Idempotent: GRANT … TO is a no-op when the membership already
    -- exists. v1 has no production caller; the GRANT exists so a future
    -- feature has the canonical seam ready.
    IF NOT EXISTS (
        SELECT 1
        FROM pg_auth_members am
        JOIN pg_roles r ON r.oid = am.roleid
        JOIN pg_roles m ON m.oid = am.member
        WHERE r.rolname = 'atlas_service_account' AND m.rolname = 'atlas_app'
    ) THEN
        GRANT atlas_service_account TO atlas_app;
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
