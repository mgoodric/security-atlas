-- security-atlas — slice 508: SCIM 2.0 user-lifecycle provisioning.
--
-- Owns ONE new tenant-scoped table + an additive widening of `users`:
--
--   - scim_credentials   : per-tenant, SCIM-scoped, admin-issued, REVOCABLE
--                          bearer credential. Mirrors api_keys (slice 034):
--                          token_hash = HMAC-SHA256(plaintext, BEARER_HASH_KEY)
--                          per docs/adr/0002-bearer-token-storage.md; plaintext
--                          returned exactly once at Issue and never persisted.
--                          DISTINCT from api_keys by design (P0-508-2): a SCIM
--                          credential authenticates ONLY the /scim/v2 endpoints;
--                          it cannot call platform /v1 APIs and cannot mint an
--                          atlas human session. The separate table IS the
--                          scope boundary — the SCIM auth middleware queries
--                          only this table, the /v1 stack only api_keys/JWT.
--
--   - users.active            : SCIM deprovision signal. true = enabled,
--                               false = deprovisioned (disabled). Deprovision is
--                               disable-not-delete (P0-508-1 / AC-4): the row is
--                               retained so the actor's historical records
--                               survive (invariant #2). Defaults true so the
--                               slice-034/108/198 INSERT paths are unchanged.
--                               `users.status` is the legacy ('active'/'disabled')
--                               text flag; `active` is the boolean SCIM mirror
--                               kept in lockstep by the SCIM store (decisions D3).
--   - users.scim_external_id  : the IdP's stable resource id (SCIM `externalId`).
--                               NULL for non-SCIM users.
--   - users.scim_managed      : true once a row is created/managed via SCIM, so
--                               the operator UI can render "managed by your IdP".
--
-- All SCIM reads/writes run under app.current_tenant RLS (invariant #6 /
-- P0-508-4): a SCIM credential for tenant A can never read or mutate tenant B.
-- The scim_credentials table follows the slice-034 four-policy RLS pattern
-- (tenant_read/write/update/delete) under FORCE ROW LEVEL SECURITY with the
-- slice-002 current_tenant_matches helper.
--
-- The bearer-auth lookup path (the SCIM auth middleware) hashes the inbound
-- token and queries scim_credentials by token_hash WITHOUT a tenant context
-- (the request has not resolved its tenant yet — the row's tenant_id is what
-- authentication RETURNS), exactly like apikeystore.Authenticate. That query
-- runs under the BYPASSRLS atlas_migrate role; the GRANT to atlas_migrate below
-- keeps that path working.
--
-- Migration slot is `20260612020000` (after slice 471's `20260612010000`).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
--
-- Issue: docs/issues/508-scim-user-lifecycle-provisioning.md
-- Reversible via 20260612020000_scim_provisioning.down.sql.

-- ===== users SCIM columns (additive) =====

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS scim_external_id TEXT NULL;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS scim_managed BOOLEAN NOT NULL DEFAULT false;

-- externalId is unique per tenant when present (an IdP never re-uses one
-- externalId for two distinct users in the same tenant). NULLs are distinct
-- so non-SCIM users (NULL externalId) do not collide.
CREATE UNIQUE INDEX IF NOT EXISTS users_scim_external_id_per_tenant_unique
    ON users (tenant_id, scim_external_id)
    WHERE scim_external_id IS NOT NULL;

-- ===== scim_credentials =====
--
-- Machine bearer credential for SCIM provisioning. token_hash is
-- HMAC-SHA256(plaintext, BEARER_HASH_KEY) per ADR 0002 — computed by the
-- application layer before INSERT. last4 is the last four chars of the
-- plaintext (safe to surface; cannot authenticate). description is an
-- operator-supplied label ("Okta production"). issued_by tracks the admin
-- user that minted it. revoked_at soft-revokes (AC-3); the auth path rejects
-- a revoked row.
CREATE TABLE scim_credentials (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    token_hash   BYTEA NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    issued_by    UUID NULL,
    issued_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NULL,
    revoked_at   TIMESTAMPTZ NULL,
    last4        TEXT NOT NULL DEFAULT '',
    CONSTRAINT scim_credentials_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT scim_credentials_token_hash_len    CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX scim_credentials_tenant_active
    ON scim_credentials (tenant_id) WHERE revoked_at IS NULL;
CREATE INDEX scim_credentials_tenant_issued
    ON scim_credentials (tenant_id, issued_at DESC);

ALTER TABLE scim_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON scim_credentials
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON scim_credentials
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON scim_credentials
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON scim_credentials
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== scim_audit_log =====
--
-- Append-only audit ledger for every SCIM provision/deprovision mutation
-- (AC-5 / STRIDE-R). Mirrors the slice-108 me_audit_log invariant: SELECT +
-- INSERT policies only. No UPDATE / DELETE policy means rows are immutable
-- once written, even by the application role.
--
-- actor_credential_id is the scim_credentials.id that performed the action
-- (the SCIM token identity, NOT an atlas user). target_user_id is the
-- provisioned/deprovisioned user. action enumerates the SCIM mutation surface.
-- detail carries the per-action shape (e.g. {"external_id":"...","active":false}).
CREATE TABLE scim_audit_log (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL,
    occurred_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_credential_id  UUID NOT NULL,
    target_user_id       UUID NULL,
    action               TEXT NOT NULL
                         CHECK (action IN (
                             'user.provision',
                             'user.replace',
                             'user.patch',
                             'user.deprovision',
                             'user.reprovision',
                             'user.delete'
                         )),
    detail               JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX scim_audit_log_tenant_occurred
    ON scim_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX scim_audit_log_target
    ON scim_audit_log (tenant_id, target_user_id, occurred_at DESC);

ALTER TABLE scim_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON scim_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON scim_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update/delete policies — append-only.

-- ===== grants =====

GRANT SELECT, INSERT, UPDATE, DELETE ON scim_credentials TO atlas_app;
-- atlas_migrate carries the BYPASSRLS lookup-by-hash path (auth) + integration
-- test cleanup, exactly like api_keys (slice 034).
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_credentials TO atlas_migrate;

-- scim_audit_log: append-only — writers + readers only; no UPDATE/DELETE grant
-- to atlas_app (the app role cannot mutate or delete an audit row).
GRANT SELECT, INSERT ON scim_audit_log TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_audit_log TO atlas_migrate;
