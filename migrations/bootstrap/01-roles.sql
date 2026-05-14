-- security-atlas — role bootstrap. Run ONCE per database before applying
-- versioned migrations.
--
-- Three roles:
--   atlas_migrate          — used by Atlas for DDL. BYPASSRLS so it can apply
--                            schema changes against tables that have FORCE
--                            ROW LEVEL SECURITY. No application code path
--                            should ever connect as this role. Slice 065
--                            also grants it CREATEROLE + ADMIN OPTION on
--                            atlas_app so first-boot can set atlas_app's
--                            password on a shared cluster — see the bug #4
--                            note below. This is the role's ONLY privilege
--                            beyond DDL, and it is scoped to atlas_app
--                            alone.
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
--
-- Slice 065 bug #4 — atlas_migrate privilege scope on a SHARED Postgres
-- cluster:
--
-- The self-host bootstrap (deploy/docker/bootstrap/bootstrap.sh phase 2.5)
-- runs `ALTER ROLE atlas_app PASSWORD '<from ATLAS_APP_PASSWORD>'` as
-- atlas_migrate so the atlas server can authenticate. On a dedicated
-- `postgres:16-alpine` container with trust auth, atlas_migrate effectively
-- stands in for the superuser and the ALTER ROLE just works. On a genuinely
-- SHARED cluster — the documented "external Postgres" upgrade path — that
-- ALTER fails with:
--
--   ERROR: permission denied to alter role
--   DETAIL: To change another role's password, the current user must have
--           the CREATEROLE attribute and the ADMIN option on the role.
--
-- So this script now (a) ensures atlas_migrate carries CREATEROLE and
-- (b) grants atlas_app TO atlas_migrate WITH ADMIN OPTION. This WIDENS
-- atlas_migrate beyond pure DDL: it may now manage atlas_app's password.
-- It does NOT let atlas_migrate escalate arbitrarily — the only role it
-- holds ADMIN OPTION on is atlas_app, so the CREATEROLE attribute cannot
-- be used to mint or take over any other role in the cluster. atlas_app
-- itself is UNCHANGED: still NOSUPERUSER NOBYPASSRLS (anti-criterion P0).
--
-- IMPORTANT for shared-cluster operators: a non-superuser role cannot grant
-- ITSELF the CREATEROLE attribute. If you pre-create atlas_migrate on a
-- shared cluster as a non-superuser, you (the cluster admin) must grant it
-- CREATEROLE up front — e.g. `ALTER ROLE atlas_migrate CREATEROLE;` run as
-- a superuser. The DO block below is conditional: when atlas_migrate
-- already has CREATEROLE the ALTER is skipped entirely, and only the
-- WITH ADMIN OPTION grant (which atlas_migrate CAN perform on itself once
-- it holds CREATEROLE) runs.

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

    -- Slice 065 bug #4: ensure atlas_migrate can manage atlas_app's
    -- password on a shared cluster. Conditional so it is a no-op when the
    -- attribute is already present (e.g. the dedicated-container case, or a
    -- shared-cluster operator who pre-granted CREATEROLE). On a shared
    -- cluster where atlas_migrate is a non-superuser WITHOUT CREATEROLE,
    -- this ALTER will itself raise `permission denied` — that is the
    -- intended signal that the cluster admin must grant CREATEROLE
    -- out-of-band first (see the header comment).
    IF NOT (SELECT rolcreaterole FROM pg_roles WHERE rolname = 'atlas_migrate') THEN
        ALTER ROLE atlas_migrate CREATEROLE;
    END IF;

    -- Grant atlas_app membership TO atlas_migrate WITH ADMIN OPTION so
    -- atlas_migrate satisfies the "ADMIN option on the role" half of the
    -- ALTER ROLE ... PASSWORD privilege check. Idempotent: skipped when the
    -- membership already exists. The ADMIN OPTION is what lets the ALTER
    -- ROLE atlas_app PASSWORD in bootstrap.sh succeed; plain membership is
    -- not enough.
    IF NOT EXISTS (
        SELECT 1
        FROM pg_auth_members am
        JOIN pg_roles r ON r.oid = am.roleid
        JOIN pg_roles m ON m.oid = am.member
        WHERE r.rolname = 'atlas_app' AND m.rolname = 'atlas_migrate'
    ) THEN
        GRANT atlas_app TO atlas_migrate WITH ADMIN OPTION;
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
