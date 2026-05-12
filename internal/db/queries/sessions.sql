-- name: CreateSession :one
-- Persist a new session row. The id is generated server-side by the caller as
-- a 32-byte crypto/rand string; we pass it in so the cookie and the row share
-- exact bytes.
INSERT INTO sessions (
    id, tenant_id, user_id, idp_issuer, idp_subject, expires_at
)
VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetSessionByID :one
-- Read a session by cookie id. Returns the row whether revoked or expired;
-- the caller checks `revoked_at IS NULL` and `expires_at > now()`.
SELECT *
FROM sessions
WHERE tenant_id = $1 AND id = $2;

-- name: TouchSession :exec
-- Update last_seen_at and (when given) bump expires_at. Caller computes the
-- new expires_at — sliding-window logic lives in the sessions package.
UPDATE sessions
SET last_seen_at = now(),
    expires_at = $3
WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL;

-- name: RevokeSession :exec
UPDATE sessions
SET revoked_at = now()
WHERE tenant_id = $1 AND id = $2;
