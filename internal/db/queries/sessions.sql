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

-- name: ListSessionsForUser :many
-- Slice 108: GET /v1/me/sessions. Returns the caller's currently-valid sessions
-- (revoked_at IS NULL AND expires_at > now()). Tenant scoped via RLS + explicit filter.
SELECT *
FROM sessions
WHERE tenant_id = $1
  AND user_id = $2
  AND revoked_at IS NULL
  AND expires_at > now()
ORDER BY last_seen_at DESC;

-- name: RevokeSessionForUser :execrows
-- Slice 108: DELETE /v1/me/sessions/{id}. Atomic ownership-guard via the WHERE clause:
-- the row is only updated when (tenant_id, id, user_id) all match. RowsAffected = 0
-- means "no such session under this user" — the handler returns 404 (not 403) to avoid
-- the existence-oracle on cross-user ids. Idempotent: re-revoking an already-revoked
-- row is a no-op but still returns 1 row affected (the row still matches the WHERE).
UPDATE sessions
SET revoked_at = COALESCE(revoked_at, now())
WHERE tenant_id = $1 AND id = $2 AND user_id = $3;

-- name: RevokeOtherSessionsForUser :execrows
-- Slice 108: DELETE /v1/me/sessions (no {id}). Revokes every valid session for the
-- caller EXCEPT the one identified by $3 (the "current" session id). When the caller
-- has no current-session cookie, pass an empty string to revoke ALL sessions for the
-- user — there is no way to keep the request alive past this point anyway.
UPDATE sessions
SET revoked_at = now()
WHERE tenant_id = $1
  AND user_id = $2
  AND id <> $3
  AND revoked_at IS NULL;
