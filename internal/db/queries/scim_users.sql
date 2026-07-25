-- name: CreateSCIMUser :one
-- Slice 508: provision a user via SCIM. Sets scim_managed=true and the IdP's
-- externalId. status + active are kept in lockstep ('active'/true). idp_issuer
-- / idp_subject are left empty here (SCIM is push-from-IdP, not OIDC login);
-- email is the join key with a later OIDC sign-in.
INSERT INTO users (
    id, tenant_id, email, display_name, status, active,
    scim_external_id, scim_managed
)
VALUES (
    $1, $2, $3, $4, 'active', true, $5, true
)
RETURNING *;

-- name: GetSCIMUserByExternalID :one
-- Lookup a SCIM-provisioned user by the IdP's externalId within the tenant.
SELECT *
FROM users
WHERE tenant_id = $1 AND scim_external_id = $2;

-- name: GetSCIMUserByEmail :one
-- Case-insensitive email lookup within a tenant. SCIM userName maps to email
-- (decisions D1); used to reconcile a SCIM Create against an existing row.
SELECT *
FROM users
WHERE tenant_id = $1 AND lower(email) = lower(sqlc.arg('email')::text);

-- name: ListSCIMUsers :many
-- Tenant-scoped user list for SCIM List (no filter). RLS confines to the
-- credential's tenant (P0-508-4). Ordered for stable pagination.
SELECT *
FROM users
WHERE tenant_id = $1
ORDER BY created_at ASC, id ASC
LIMIT $2 OFFSET $3;

-- name: CountSCIMUsers :one
SELECT COUNT(*) FROM users WHERE tenant_id = $1;

-- name: ListSCIMUsersByUserName :many
-- SCIM List with `filter=userName eq "x"` (AC-1). userName maps to email.
SELECT *
FROM users
WHERE tenant_id = $1 AND lower(email) = lower(sqlc.arg('email')::text)
ORDER BY created_at ASC, id ASC;

-- name: ReplaceSCIMUser :one
-- SCIM Replace (PUT) — overwrites the mutable SCIM-mapped attributes:
-- display_name, active (and the mirrored status), email. external_id and
-- roles are NOT touched here (P0-508-3: SCIM never assigns roles).
UPDATE users
SET display_name = $3,
    email        = $4,
    active       = $5,
    status       = CASE WHEN $5 THEN 'active' ELSE 'disabled' END,
    updated_at   = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: SetSCIMUserActive :one
-- SCIM Patch `active` flip (AC-4) — the deprovision/reprovision signal. Keeps
-- status in lockstep with the boolean. Never touches roles.
UPDATE users
SET active     = $3,
    status     = CASE WHEN $3 THEN 'active' ELSE 'disabled' END,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: PatchSCIMUserDisplayName :one
-- SCIM Patch of a core attribute (display_name). Roles are out of scope.
UPDATE users
SET display_name = $3,
    updated_at   = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: RevokeAllSCIMUserSessions :execrows
-- Deprovision (AC-4): revoke EVERY valid session for the user so the
-- deprovisioned account loses all access immediately. No keep-id — unlike the
-- /v1/me "sign out other devices" path, deprovision kills the whole set.
UPDATE sessions
SET revoked_at = now()
WHERE tenant_id = $1
  AND user_id = $2
  AND revoked_at IS NULL;
