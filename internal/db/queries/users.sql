-- name: CreateUser :one
-- Insert a new user (local-mode). For OIDC-provisioned users use UpsertUserByIdpSubject
-- which also handles tenant_id, display_name, and the idp_* fields.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetUserByEmail :one
-- Lookup by case-insensitive email within a tenant. Used by /auth/local/login.
SELECT *
FROM users
WHERE tenant_id = $1 AND lower(email) = lower(sqlc.arg('email')::text);

-- name: GetUserByID :one
SELECT *
FROM users
WHERE tenant_id = $1 AND id = $2;

-- name: UpsertUserByIdpSubject :one
-- Used by the OIDC callback: provision-on-first-sign-in by (idp_issuer, idp_subject).
-- The composite UNIQUE index on (idp_issuer, idp_subject) WHERE both non-empty
-- is the conflict target. On conflict we update display_name + email (the IdP
-- is canonical) and updated_at.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> ''
DO UPDATE SET
    email = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    updated_at = now()
RETURNING *;
