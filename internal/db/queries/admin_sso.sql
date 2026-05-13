-- Slice 062 — admin /v1/admin/sso queries.
--
-- The admin SSO endpoint upserts a single oidc_idp_configs row per tenant
-- keyed by name. The v1 contract surfaces exactly one IdP per tenant — the
-- "primary" config — by convention using name='primary'. Multi-IdP support
-- is a v2 conversation (the underlying table already supports many rows
-- per tenant; the v1 HTTP surface is single-config to keep the UI simple).
--
-- client_secret is NEVER returned in GET (slice 034 AC-9 contract); the
-- GetAdminSSO query intentionally omits client_secret_enc from the
-- response. PatchAdminSSO accepts the secret encoded as bytea — empty
-- means "leave existing"; the application interprets nil/empty as
-- secret-unchanged.

-- name: GetAdminSSO :one
-- Returns the tenant's primary OIDC IdP config (by name). Omits the
-- encrypted client secret from the response shape so a leaked log line
-- never carries the secret material.
SELECT id,
       tenant_id,
       name,
       issuer_url,
       client_id,
       redirect_url,
       allowed_email_domains,
       created_at,
       updated_at
FROM oidc_idp_configs
WHERE tenant_id = $1 AND name = $2;

-- name: UpsertAdminSSO :one
-- Insert-or-update the tenant's primary IdP config. The application
-- supplies the encrypted client_secret_enc; an empty bytea is rejected
-- at the application layer for INSERT but permitted for UPDATE-only
-- with secret-unchanged semantics. id is supplied by the caller (UUIDv4)
-- so the insert path is deterministic in tests.
INSERT INTO oidc_idp_configs (
    id, tenant_id, name, issuer_url, client_id,
    client_secret_enc, redirect_url, allowed_email_domains,
    created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    now(), now()
)
ON CONFLICT (tenant_id, name) DO UPDATE
SET issuer_url            = EXCLUDED.issuer_url,
    client_id             = EXCLUDED.client_id,
    -- Secret update only when caller supplies a non-empty bytea.
    -- Empty bytea is the leave-existing sentinel.
    client_secret_enc     = CASE
        WHEN octet_length(EXCLUDED.client_secret_enc) > 0 THEN EXCLUDED.client_secret_enc
        ELSE oidc_idp_configs.client_secret_enc
    END,
    redirect_url          = EXCLUDED.redirect_url,
    allowed_email_domains = EXCLUDED.allowed_email_domains,
    updated_at            = now()
RETURNING id,
          tenant_id,
          name,
          issuer_url,
          client_id,
          redirect_url,
          allowed_email_domains,
          created_at,
          updated_at;
