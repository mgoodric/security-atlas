-- name: CreateOidcIdpConfig :one
INSERT INTO oidc_idp_configs (
    id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url, allowed_email_domains
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetOidcIdpConfigByName :one
SELECT *
FROM oidc_idp_configs
WHERE tenant_id = $1 AND name = $2;

-- name: ListOidcIdpConfigsByTenant :many
SELECT *
FROM oidc_idp_configs
WHERE tenant_id = $1
ORDER BY name ASC;
