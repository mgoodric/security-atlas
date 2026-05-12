-- name: InsertAPIKey :one
-- Persist a new API key. token_hash is HMAC-SHA256(plaintext, BEARER_HASH_KEY)
-- per ADR 0002 — computed by the application layer before this call. last4 is
-- the last four characters of the plaintext bearer (safe to surface).
INSERT INTO api_keys (
    id, tenant_id, token_hash, scope_predicate, allowed_kinds,
    issued_by, expires_at, rotated_from,
    is_admin, is_approver, owner_roles, last4, ttl_seconds
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12, $13
)
RETURNING *;

-- name: GetAPIKeyByHash :one
-- Constant-time lookup by HMAC hash. Returns the row whether revoked, retired,
-- or expired — the caller (credstore.Authenticate) is responsible for the
-- state-check tree.
SELECT *
FROM api_keys
WHERE token_hash = $1;

-- name: GetAPIKeyByID :one
SELECT *
FROM api_keys
WHERE tenant_id = $1 AND id = $2;

-- name: ListAPIKeysByTenant :many
-- Active keys for a tenant. Excludes revoked rows; includes retired-but-not-yet-
-- past-grace predecessors so the admin UI can show "rotating out — valid until X."
SELECT *
FROM api_keys
WHERE tenant_id = $1 AND revoked_at IS NULL
ORDER BY issued_at DESC;

-- name: TouchAPIKeyLastUsed :exec
-- Best-effort timestamp bump. Failure here is logged but never blocks the
-- authenticated request.
UPDATE api_keys
SET last_used_at = now()
WHERE token_hash = $1;

-- name: RevokeAPIKey :exec
UPDATE api_keys
SET revoked_at = now()
WHERE tenant_id = $1 AND id = $2;

-- name: SetAPIKeyRetiresAt :exec
-- Set the predecessor's retirement deadline on rotation. After retires_at the
-- predecessor's bearer no longer authenticates (credstore.Authenticate enforces).
UPDATE api_keys
SET retires_at = $3
WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL;
