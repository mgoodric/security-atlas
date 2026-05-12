-- security-atlas — users, local credentials, sessions, OIDC IdP configs, API keys (slice 034).
--
-- Owns the five tables that back authentication:
--   - users               : tenant-scoped principal identity (mapped from OIDC or local)
--   - local_credentials   : argon2id-hashed passwords for solo deployments
--   - sessions            : server-side opaque session ids (cookie-bound)
--   - oidc_idp_configs    : per-tenant IdP relationship (issuer + client + allowed domains)
--   - api_keys            : machine bearer credentials, HMAC-SHA256 hashed (see docs/adr/0002-bearer-token-storage.md)
--
-- All five tables follow the slice-005-era four-policy RLS pattern (tenant_read/write/update/delete)
-- under FORCE ROW LEVEL SECURITY, with the slice-002 `current_tenant_matches` helper.
--
-- Migration slot is `20260511000012`; slice 021 owns `_000011`.
--
-- Canvas: §9.5 (auth model). Roadmap: §10.1 (Auth row).
-- Issue: docs/issues/034-oidc-rp-local-users.md

-- ===== users =====
--
-- Identity scoped to a tenant. An IdP user from `iss + sub` lands here; a local-mode
-- user (solo deployment) is created via admin bootstrap and binds to local_credentials.
-- email is unique per tenant (case-insensitive); same email may exist across tenants.
-- idp_issuer + idp_subject jointly identify an OIDC principal; both NULL for local users.
CREATE TABLE users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    email        TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'disabled')),
    idp_issuer   TEXT NOT NULL DEFAULT '',
    idp_subject  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_email_per_tenant_unique UNIQUE (tenant_id, email)
);

-- A user is uniquely identified across the platform by (idp_issuer, idp_subject)
-- when both are non-empty. Empty pair means "local user — no IdP backing."
CREATE UNIQUE INDEX users_idp_principal_unique
    ON users (idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> '';

CREATE INDEX users_tenant_status ON users (tenant_id, status);

-- ===== local_credentials =====
--
-- Argon2id-hashed passwords for solo deployments. One row per user_id (no
-- multi-credential support in v1). tenant_id is denormalized so RLS is symmetric
-- with the other four tables in this migration. password_hash carries the
-- encoded argon2id form `$argon2id$v=19$m=...$...$...` (RFC 9106 string format).
CREATE TABLE local_credentials (
    user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    tenant_id     UUID NOT NULL,
    password_hash TEXT NOT NULL,
    algo          TEXT NOT NULL DEFAULT 'argon2id',
    params        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== sessions =====
--
-- Server-side session table keyed on the cookie's opaque id (text PK). The
-- cookie value is the raw id string; we never persist anything client-derivable
-- as a session identifier without server storage. expires_at + sliding-window
-- refresh extend the session; revoked_at is set by /auth/logout (soft delete).
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    tenant_id    UUID NOT NULL,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idp_issuer   TEXT NOT NULL DEFAULT '',
    idp_subject  TEXT NOT NULL DEFAULT '',
    issued_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ NULL
);

CREATE INDEX sessions_user ON sessions (tenant_id, user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expires ON sessions (expires_at) WHERE revoked_at IS NULL;

-- ===== oidc_idp_configs =====
--
-- Per-tenant OIDC relying-party config. Multiple IdPs per tenant are supported
-- (one row per IdP). v1 stores client_secret as raw bytea — KMS-wrap is a v1.x
-- task (the slice docs the limitation; deployments using shared secrets should
-- treat the DB as a secret material itself).
CREATE TABLE oidc_idp_configs (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL,
    name                  TEXT NOT NULL,
    issuer_url            TEXT NOT NULL,
    client_id             TEXT NOT NULL,
    client_secret_enc     BYTEA NOT NULL,
    redirect_url          TEXT NOT NULL,
    allowed_email_domains TEXT[] NOT NULL DEFAULT '{}'::text[],
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT oidc_idp_configs_name_per_tenant_unique UNIQUE (tenant_id, name)
);

CREATE INDEX oidc_idp_configs_tenant ON oidc_idp_configs (tenant_id);

-- ===== api_keys =====
--
-- Machine bearer credentials. token_hash is HMAC-SHA256(plaintext, BEARER_HASH_KEY)
-- per ADR 0002. Plaintext bearer is returned exactly once at Issue or Rotate
-- and never persisted. scope_predicate is a free-form JSON object the slice-013
-- ingest handler evaluates; allowed_kinds restricts which evidence_kind values
-- this key may push. issued_by tracks the user (or principal) that minted the
-- key. rotated_from tracks the predecessor when minted via Rotate; the
-- predecessor remains valid for a grace window past its retires_at (set when
-- Rotate fires; not represented as a column because the application's
-- now() + grace check uses (rotated_from IS NOT NULL ? issued_at + grace).
--
-- is_admin / is_approver / owner_roles preserve the slice-014/018/011 flag
-- model; slice 035 will graduate this to OPA-driven RBAC.
CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    token_hash      BYTEA NOT NULL,
    scope_predicate JSONB NOT NULL DEFAULT '{}'::jsonb,
    allowed_kinds   TEXT[] NOT NULL DEFAULT '{}'::text[],
    issued_by       UUID NULL,
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NULL,
    last_used_at    TIMESTAMPTZ NULL,
    revoked_at      TIMESTAMPTZ NULL,
    rotated_from    UUID NULL REFERENCES api_keys(id) ON DELETE SET NULL,
    retires_at      TIMESTAMPTZ NULL,
    is_admin        BOOLEAN NOT NULL DEFAULT false,
    is_approver     BOOLEAN NOT NULL DEFAULT false,
    owner_roles     TEXT[] NOT NULL DEFAULT '{}'::text[],
    last4           TEXT NOT NULL DEFAULT '',
    ttl_seconds     BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT api_keys_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT api_keys_token_hash_len    CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX api_keys_tenant_active ON api_keys (tenant_id) WHERE revoked_at IS NULL;
CREATE INDEX api_keys_tenant_issued ON api_keys (tenant_id, issued_at DESC);

-- ===== Row-Level Security: four-policy split for every tenant-scoped table =====

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON users
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON users
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON users
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON users
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE local_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE local_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON local_credentials
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON local_credentials
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON local_credentials
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON local_credentials
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON sessions
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON sessions
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON sessions
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON sessions
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE oidc_idp_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE oidc_idp_configs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON oidc_idp_configs
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON oidc_idp_configs
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON oidc_idp_configs
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON oidc_idp_configs
    FOR DELETE USING (current_tenant_matches(tenant_id));

ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON api_keys
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON api_keys
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON api_keys
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON api_keys
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON
    users, local_credentials, sessions, oidc_idp_configs, api_keys
TO atlas_app;

-- The bearer-auth lookup path (apikeystore.Authenticate) hashes the inbound
-- token and queries `api_keys` by token_hash WITHOUT a tenant context (the
-- request hasn't resolved its tenant yet — the row's tenant_id is what
-- authentication RETURNS). That query runs under the BYPASSRLS `atlas_migrate`
-- role per docs/adr/0002-bearer-token-storage.md. Grant DML on every table
-- here to atlas_migrate so the auth path can lookup-by-hash + integration
-- tests can clean up via the admin pool. Owners of newly-created tables vary
-- by deployment; the explicit GRANT keeps the migration self-contained.
GRANT SELECT, INSERT, UPDATE, DELETE ON
    users, local_credentials, sessions, oidc_idp_configs, api_keys
TO atlas_migrate;
