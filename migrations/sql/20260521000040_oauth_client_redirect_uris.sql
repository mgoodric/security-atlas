-- security-atlas — slice 189: OAuth client redirect-URI registry.
--
-- Adds the `oauth_client_redirect_uris` table — the open-redirect
-- prevention gate for `GET /oauth/authorize`. Per RFC 6749 §10.6, an
-- authorization server MUST validate the supplied `redirect_uri`
-- against a per-client registered list. The handler rejects any
-- request whose `redirect_uri` does not match a row in this table.
--
-- TENANCY NOTE — INTENTIONAL DEVIATION:
--
-- This table is NOT tenant-scoped. Redirect URIs are per-client-
-- global (each `client_id` has its own registered URI list, but the
-- client_id is itself platform-global per the slice-188
-- oauth_clients design). Adding tenant_id here would force every
-- tenant admin to re-register the URI list, contradicting the
-- "client is a platform-global identity" decision locked in slice
-- 188.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation at the DB layer — preserved by the JWT
--      minted via the redirect carrying tenant scope from the user's
--      session, not from the URI registry.
--
-- Anti-criteria honored at the schema layer:
--
--   - UNIQUE on (client_id, redirect_uri) — duplicate registrations
--     rejected at the DB layer, so the CLI's add-redirect-uri command
--     surfaces a clean "already registered" error rather than a
--     silent duplicate row.
--   - CHECK on redirect_uri non-empty.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE is a fresh schema; the down-migration drops it.

CREATE TABLE oauth_client_redirect_uris (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The client_id whose redirect URIs this row registers. NOT a
    -- foreign key to oauth_clients: a client deletion in the future
    -- should NOT silently strip its registered URIs (operators may
    -- want to audit revoked clients). Cleanup is a follow-on slice.
    client_id       TEXT NOT NULL,

    -- The exact URI the authorize handler will accept on a request
    -- carrying `redirect_uri=<this value>`. Exact-match — no
    -- wildcards, no trailing-slash flex, no query-string-ignore.
    -- A client that needs N variant URIs registers N rows.
    redirect_uri    TEXT NOT NULL,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- UNIQUE on the natural key prevents duplicate registrations and
    -- gives the lookup-by-(client_id, redirect_uri) hot path a
    -- B-tree index for free.
    CONSTRAINT oauth_client_redirect_uris_natural_key UNIQUE (client_id, redirect_uri),

    CONSTRAINT oauth_client_redirect_uris_uri_nonempty
        CHECK (length(redirect_uri) > 0),
    CONSTRAINT oauth_client_redirect_uris_client_id_nonempty
        CHECK (length(client_id) > 0)
);

-- RLS: NOT enabled (see header comment). The table holds
-- per-client-global redirect URIs.

-- atlas_app: SELECT for the authorize handler's lookup, INSERT for
-- the CLI's add-redirect-uri command. DELETE for an eventual
-- remove-redirect-uri command (CLI surface in a follow-on slice;
-- grant given here so the future addition doesn't need a migration).
GRANT SELECT, INSERT, DELETE ON oauth_client_redirect_uris TO atlas_app;
