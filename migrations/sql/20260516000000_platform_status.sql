-- security-atlas — platform_status singleton (slice 073).
--
-- Implements docs/issues/073-first-time-login-ux.md AC-1.
--
-- ----------------------------------------------------------------------------
-- WHAT THIS IS
-- ----------------------------------------------------------------------------
-- A single-row, tenant-agnostic table that records two facts about the
-- platform itself:
--
--   first_signin_at              when the first successful sign-in occurred
--                                (NULL on a fresh install; set exactly once,
--                                never updated afterwards)
--   bootstrap_token_consumed_at  when the bootstrap-token file was deleted by
--                                the platform on first sign-in
--
-- The table is the platform's "is this a fresh install?" marker. The public
-- GET /v1/install-state endpoint reads first_signin_at IS NULL and the
-- login page swaps its copy accordingly (AC-2..AC-5).
--
-- ----------------------------------------------------------------------------
-- TENANCY (P0-A7)
-- ----------------------------------------------------------------------------
-- This table is INTENTIONALLY outside the tenancy model — it describes the
-- platform, not a tenant within it. There is no tenant_id column at all.
-- A multi-tenant install flips the marker exactly once (on the first sign-in
-- by any user in any tenant), not N times. Same shape as schema_migrations
-- (the bootstrap.sh "operational metadata, not tenant data" ledger).
--
-- ----------------------------------------------------------------------------
-- SINGLETON CONSTRAINT
-- ----------------------------------------------------------------------------
-- singleton_lock BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton_lock IS TRUE)
-- admits exactly one row. Any second INSERT collides on the primary key and
-- fails. Any attempt to INSERT with singleton_lock = FALSE fails the CHECK.
-- This is the canonical "one row only" idiom — see also Postgres's pg_database
-- pattern and the well-known "Postgres anti-patterns" guidance.
--
-- ----------------------------------------------------------------------------
-- RLS (slice 068 pattern, adapted for no-tenant singleton)
-- ----------------------------------------------------------------------------
-- The table has ENABLE + FORCE ROW LEVEL SECURITY. Two policies:
--
--   public_read   FOR SELECT USING (true)
--                 every authenticated and bearer-exempt context can read.
--                 The endpoint that backs this is intentionally public — same
--                 precedent as /health (slice 037) and /v1/version (slice 072
--                 parallel). "Is this a fresh install?" is platform metadata,
--                 not tenant data.
--
--   (no write policies for atlas_app)
--                 Under FORCE ROW LEVEL SECURITY, the absence of an INSERT /
--                 UPDATE / DELETE policy means atlas_app cannot mutate the row
--                 at all. Writes must go through the migrate role (BYPASSRLS)
--                 — the same pool the schema importer and the scheduler loops
--                 use. The atlas server's elevated write path (the
--                 /v1/install/mark-first-signin handler, run as the migrate
--                 pool) is the only way the row is updated at runtime; the
--                 --reset-bootstrap CLI uses the migrate pool too.
--
-- This split prevents a bug in any app-pool handler from accidentally flipping
-- the marker. The marker is platform-wide and authoritative; only an
-- intentional elevated-path write should touch it.
--
-- ----------------------------------------------------------------------------
-- SEED ROW
-- ----------------------------------------------------------------------------
-- The migration inserts the singleton row with both timestamps NULL. Without
-- this seed, the platform's "is fresh install?" read would have to first-run
-- create the row, complicating the read path. Pre-seeding makes the read a
-- pure SELECT with no INSERT path.

CREATE TABLE platform_status (
    singleton_lock              BOOLEAN PRIMARY KEY DEFAULT TRUE,
    first_signin_at             TIMESTAMPTZ NULL,
    bootstrap_token_consumed_at TIMESTAMPTZ NULL,
    CONSTRAINT platform_status_singleton CHECK (singleton_lock IS TRUE)
);

INSERT INTO platform_status (singleton_lock) VALUES (TRUE);

ALTER TABLE platform_status ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_status FORCE ROW LEVEL SECURITY;

-- Public read: every connecting role may see the singleton row. The row
-- contains no tenant data, no PII, no secret; it is platform metadata.
CREATE POLICY public_read ON platform_status
    FOR SELECT
    USING (true);

-- atlas_app may read the singleton row but cannot write it. The platform's
-- elevated write paths (the mark-first-signin handler, the reset-bootstrap
-- CLI) run as atlas_migrate (BYPASSRLS) and therefore bypass the absence of
-- write policies here. This is the load-bearing safety property: a buggy
-- atlas_app handler cannot accidentally flip first_signin_at.
GRANT SELECT ON platform_status TO atlas_app;
