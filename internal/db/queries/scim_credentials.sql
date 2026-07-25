-- name: InsertSCIMCredential :one
-- Slice 508: persist a new SCIM provisioning credential. token_hash is
-- HMAC-SHA256(plaintext, BEARER_HASH_KEY) per ADR 0002 — computed by the
-- application layer before this call. last4 is the last four chars of the
-- plaintext bearer (safe to surface).
INSERT INTO scim_credentials (
    id, tenant_id, token_hash, description, issued_by, last4
)
VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetSCIMCredentialByHash :one
-- Lookup by HMAC hash for the SCIM auth middleware. Runs under the BYPASSRLS
-- atlas_migrate role (no tenant context yet — the row's tenant_id is what
-- authentication RETURNS). Returns the row whether revoked or not — the caller
-- enforces the revoked-state check.
SELECT *
FROM scim_credentials
WHERE token_hash = $1;

-- name: GetSCIMCredentialByID :one
SELECT *
FROM scim_credentials
WHERE tenant_id = $1 AND id = $2;

-- name: ListSCIMCredentialsByTenant :many
-- Active SCIM credentials for a tenant (excludes revoked).
SELECT *
FROM scim_credentials
WHERE tenant_id = $1 AND revoked_at IS NULL
ORDER BY issued_at DESC;

-- name: TouchSCIMCredentialLastUsed :exec
-- Best-effort timestamp bump; failure logged, never blocks the request.
UPDATE scim_credentials
SET last_used_at = now()
WHERE token_hash = $1;

-- name: RevokeSCIMCredential :exec
UPDATE scim_credentials
SET revoked_at = now()
WHERE tenant_id = $1 AND id = $2;

-- name: InsertSCIMAuditLog :exec
-- Append-only audit row for a SCIM provision/deprovision mutation (AC-5).
INSERT INTO scim_audit_log (
    tenant_id, actor_credential_id, target_user_id, action, detail
)
VALUES (
    $1, $2, $3, $4, $5
);

-- name: ListSCIMAuditLogByTenant :many
-- Read-only: SCIM audit rows for a tenant, newest first. Backs integration
-- assertions (AC-5) and any future admin surface.
SELECT *
FROM scim_audit_log
WHERE tenant_id = $1
ORDER BY occurred_at DESC
LIMIT $2;
