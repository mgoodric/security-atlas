-- security-atlas — slice 188: OAuth client registration table.
--
-- Adds the `oauth_clients` table — the registry of machine clients that
-- can acquire JWT access tokens from `POST /oauth/token` via the
-- `grant_type=client_credentials` flow (RFC 6749 §4.4). Slice 191's
-- per-language SDK migration moves SDK acquisition from the slice-034
-- api_keys table onto this surface.
--
-- TENANCY NOTE — INTENTIONAL DEVIATION FROM THE CANONICAL FOUR-POLICY
-- RLS PATTERN:
--
-- `oauth_clients` is NOT tenant-scoped. Per slice 188's design
-- (docs/issues/188-...md, AC-1 + Plans/canvas/09-tech-stack.md
-- Authorization Server row), machine clients are platform-global
-- identities — a client_id authenticates to the atlas issuer; the JWT
-- it receives carries tenant scope via the `atlas:current_tenant_id`
-- claim, which is set per-token (NOT per-client). Adding a tenant_id
-- column here would force a 1:1 client-to-tenant binding and break the
-- tenant-switching token-exchange semantics that slice 192 wires.
--
-- The trade-off is that any super_admin operator (or the maintainer at
-- self-host scale) can issue a client; there is no per-tenant
-- self-service issuance in v1. When per-tenant client issuance
-- self-service becomes a requirement, the migration extending this
-- table will add `tenant_id UUID NULL` (NULL = platform-global) + a
-- mixed-mode RLS policy. v1 keeps the table simple.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation at the DB layer — preserved by oauth_clients
--      having NO tenant-scoped data. The token-exchange audit table
--      (separate migration 20260521000020) IS tenant-scoped via RLS.
--   #10 Audit-period freezing — N/A; this table holds identities, not
--      evidence.
--
-- Anti-criteria honored at the schema layer:
--
--   - No plaintext secret column (P0-188-3): only `client_secret_hash`
--     is stored. The argon2id-encoded form is the standard
--     `$argon2id$v=19$m=...$...$...` string produced by
--     internal/auth/oauthclient.
--   - UNIQUE on `client_id` (AC-1) — duplicate issuance is rejected at
--     the DB layer, not just the application layer.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE is a fresh schema; the down-migration drops the table
--   cleanly. No data-preservation step is required because v1 has no
--   in-flight clients to migrate.

CREATE TABLE oauth_clients (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The opaque public client identifier presented in the `client_id`
    -- form param at `POST /oauth/token`. Generated as a UUIDv4 string
    -- by the issuance path (internal/auth/oauthclient.Issue) — kept as
    -- TEXT not UUID so future issuance paths can emit shorter,
    -- vendor-style identifiers without a migration.
    client_id           TEXT NOT NULL,

    -- Argon2id-encoded form of the 32-byte client_secret. The
    -- plaintext secret is returned to the operator EXACTLY ONCE at
    -- issuance and never persisted. See internal/auth/oauthclient for
    -- the argon2id parameter choice + rationale (D1).
    client_secret_hash  TEXT NOT NULL,

    -- Human-friendly label so operators can identify the client in the
    -- admin UI / audit log. UNIQUE so the CLI's --name flag is a
    -- reliable lookup key (AC-3: exit 1 on duplicate name).
    name                TEXT NOT NULL,

    -- Soft-disable: setting this column non-NULL revokes the client
    -- without deleting the row, preserving forensic linkage from
    -- oauth_token_exchanges.subject_token_sub back to the issuer.
    -- Slice 190 will add /oauth/revoke (revokes individual tokens);
    -- this column is the long-lived disable for the client identity
    -- itself.
    disabled_at         TIMESTAMPTZ NULL,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- UNIQUE on client_id: defense-in-depth against an issuance bug
    -- that races and writes the same UUID twice. PRIMARY KEY is `id`
    -- so the natural-key UNIQUE is a separate constraint.
    CONSTRAINT oauth_clients_client_id_unique UNIQUE (client_id),

    -- UNIQUE on name: AC-3 "EXIT 1 on duplicate name" is enforced at
    -- the DB layer, not just the application layer, so a race between
    -- two concurrent CLI invocations can't both succeed.
    CONSTRAINT oauth_clients_name_unique UNIQUE (name),

    -- Defense-in-depth: a NULL or empty client_secret_hash would be
    -- a catastrophic bypass — the application must always write the
    -- argon2id-encoded form. The encoded form is ~90+ chars; require
    -- length > 20 as a sanity floor (any real argon2id encoding is
    -- well above that).
    CONSTRAINT oauth_clients_secret_hash_nonempty
        CHECK (length(client_secret_hash) > 20)
);

-- Index supporting the hot-path lookup `WHERE client_id = $1 AND
-- disabled_at IS NULL`. The token endpoint authenticates against this
-- predicate on every request; the UNIQUE constraint alone gives a
-- B-tree index but not the disabled_at filter, so the composite is
-- justified.
CREATE INDEX idx_oauth_clients_active_lookup
    ON oauth_clients (client_id)
    WHERE disabled_at IS NULL;

-- RLS: NOT enabled (see header comment). The table holds
-- platform-global identities. Only super_admin operators and the
-- atlas-cli `oauth issue-client` command write here in v1. Future
-- per-tenant issuance will add `tenant_id NULL` + a mixed-mode RLS
-- policy in a follow-on migration; v1 stays simple.

-- atlas_app: SELECT for the token endpoint's authentication lookup,
-- INSERT for the CLI's issuance path (the CLI runs through the
-- application user, not atlas_migrate). UPDATE for soft-disable in a
-- follow-on slice; DELETE not granted (use disabled_at).
GRANT SELECT, INSERT, UPDATE ON oauth_clients TO atlas_app;
