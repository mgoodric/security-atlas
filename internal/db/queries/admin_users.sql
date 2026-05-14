-- Slice 062 — admin /v1/admin/users queries.
--
-- Three queries:
--   - ListAdminUsers      : paginated list with roles aggregated via array_agg
--   - GetAdminUser        : single-user detail with roles
--   - DeleteUserRoles     : clears all role assignments for a user
--   - InsertUserRole      : adds a single role assignment
--
-- Role replacement is a (DELETE + INSERTs) pair under the application's
-- transaction. The role enum lives in user_roles' CHECK constraint
-- (slice 035 migration); the application validates the requested role
-- against authz.IsCanonical before calling InsertUserRole so the DB CHECK
-- is a backstop, not the primary gate.
--
-- Pagination uses cursor (last user id seen) + limit. The cursor is an
-- opaque base64 of the last row's id; the application encodes / decodes.
--
-- The session table carries last_seen_at; for the v1 admin/users response
-- we surface the max(sessions.last_seen_at) per user as last_login_at,
-- joined LEFT so users who have never logged in still appear.

-- name: ListAdminUsers :many
-- Paginated list of users with their roles. Joins user_roles via a
-- correlated subquery so the role array is empty (not null) for
-- role-less users. last_login_at is the max(sessions.last_seen_at) for
-- non-revoked sessions; NULL when no session row exists.
SELECT
    u.id,
    u.email,
    u.display_name,
    u.status,
    u.created_at,
    u.updated_at,
    (SELECT max(s.last_seen_at)::timestamptz
       FROM sessions s
      WHERE s.tenant_id = u.tenant_id
        AND s.user_id   = u.id
        AND s.revoked_at IS NULL)::timestamptz AS last_login_at,
    COALESCE(
        (SELECT array_agg(ur.role ORDER BY ur.role)
           FROM user_roles ur
          WHERE ur.tenant_id = u.tenant_id
            AND ur.user_id   = u.id::text),
        ARRAY[]::text[]
    )::text[] AS roles
FROM users u
WHERE u.tenant_id = $1
  AND (sqlc.arg('cursor_after')::uuid IS NULL OR u.id > sqlc.arg('cursor_after')::uuid)
ORDER BY u.id ASC
LIMIT $2;

-- name: GetAdminUser :one
-- Returns a single user with their roles and most-recent session timestamp.
-- 404 (pgx.ErrNoRows) when the id is not in the tenant.
SELECT
    u.id,
    u.email,
    u.display_name,
    u.status,
    u.created_at,
    u.updated_at,
    (SELECT max(s.last_seen_at)::timestamptz
       FROM sessions s
      WHERE s.tenant_id = u.tenant_id
        AND s.user_id   = u.id
        AND s.revoked_at IS NULL)::timestamptz AS last_login_at,
    COALESCE(
        (SELECT array_agg(ur.role ORDER BY ur.role)
           FROM user_roles ur
          WHERE ur.tenant_id = u.tenant_id
            AND ur.user_id   = u.id::text),
        ARRAY[]::text[]
    )::text[] AS roles
FROM users u
WHERE u.tenant_id = $1
  AND u.id        = $2;

-- name: DeleteUserRoles :exec
-- Clears every role assignment for a (tenant, user). Paired with one or
-- more InsertUserRole calls in the same transaction to implement
-- "replace the role set" semantics for PATCH .../roles.
DELETE FROM user_roles
WHERE tenant_id = $1 AND user_id = $2;

-- name: InsertUserRole :exec
-- Adds a single (tenant, user, role) assignment. Idempotent under the
-- composite PK; conflicts are silently no-op so concurrent admins
-- granting the same role don't fail.
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, user_id, role) DO NOTHING;
