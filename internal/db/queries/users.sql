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

-- name: UpdateUserProfile :one
-- Slice 108: PATCH /v1/me. Updates display_name + time_zone only (email + idp_subject
-- are read-only — managed by the IdP). Empty-string sentinel pattern: pass the existing
-- value to leave the column unchanged. The handler builds the diff from the request
-- body before calling this query.
UPDATE users
SET display_name = $3,
    time_zone    = $4,
    updated_at   = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: UpsertUserByIdpSubject :one
-- Used by the OIDC callback: provision-on-first-sign-in by (idp_issuer, idp_subject).
-- The per-tenant UNIQUE index users_idp_principal_unique on (tenant_id,
-- idp_issuer, idp_subject) WHERE both non-empty
-- is the conflict target. On conflict we update display_name + email (the IdP
-- is canonical) and updated_at.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (tenant_id, idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> ''
DO UPDATE SET
    email = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    updated_at = now()
RETURNING *;

-- name: LinkUserIdpByEmail :one
-- #1944: first OIDC sign-in for a user SCIM (or local mode) already created.
-- Binds the IdP principal to the same-tenant row with the same email
-- (case-insensitive, matching GetSCIMUserByEmail) ONLY while that row's idp
-- tuple is empty, so a row already bound to another principal is never
-- re-linked. The NOT EXISTS keeps a returning user on their own row. Status
-- and active are left untouched: a deprovisioned row stays refused.
UPDATE users
SET idp_issuer   = sqlc.arg('idp_issuer'),
    idp_subject  = sqlc.arg('idp_subject'),
    display_name = sqlc.arg('display_name'),
    updated_at   = now()
WHERE users.tenant_id = sqlc.arg('tenant_id')
  AND lower(users.email) = lower(sqlc.arg('email')::text)
  AND users.idp_issuer = ''
  AND users.idp_subject = ''
  AND NOT EXISTS (
      SELECT 1 FROM users u
      WHERE u.tenant_id = sqlc.arg('tenant_id')
        AND u.idp_issuer = sqlc.arg('idp_issuer')
        AND u.idp_subject = sqlc.arg('idp_subject')
  )
RETURNING *;

-- name: OIDCFirstSignInEmailTaken :one
-- #1944: true when this IdP principal has no row yet but another same-tenant
-- row holds the email (case-insensitive). UpsertOIDC asks only after
-- LinkUserIdpByEmail declined, so that row is bound to another principal or
-- the link was refused; the sign-in must not mint a second user for the
-- email. users_email_per_tenant_unique is case-sensitive and would miss a
-- case variant. A returning principal is never refused here.
SELECT (
    EXISTS (
        SELECT 1 FROM users u
        WHERE u.tenant_id = sqlc.arg('tenant_id')
          AND lower(u.email) = lower(sqlc.arg('email')::text)
    )
    AND NOT EXISTS (
        SELECT 1 FROM users u
        WHERE u.tenant_id = sqlc.arg('tenant_id')
          AND u.idp_issuer = sqlc.arg('idp_issuer')
          AND u.idp_subject = sqlc.arg('idp_subject')
    )
)::boolean AS taken;
