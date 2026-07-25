-- Slice 468: per-(tenant, user) saved filter-views for the /controls list.
--
-- The TENANT half of isolation is RLS (current_tenant_matches on every
-- policy); the USER half is the mandatory user_id predicate on every query
-- here, sourced from the verified credential in the handler — NEVER the
-- request body (threat-model I / P0-448-5). There is no app.current_user
-- GUC at v1, so the user cut lives in the WHERE clause, exactly as
-- user_notification_preferences (slice 016) does it.

-- name: ListSavedViews :many
-- A user's saved views for a surface, oldest-first (stable display order).
-- user_id is ALWAYS the calling credential's id — a caller can never pass
-- another user's id to read their views.
SELECT id, tenant_id, user_id, surface, name, filters, created_at, updated_at
FROM saved_views
WHERE tenant_id = $1 AND user_id = $2 AND surface = $3
ORDER BY created_at ASC, id ASC;

-- name: CountSavedViews :one
-- The user's saved-view count for a surface — used to enforce the per-user
-- cap before INSERT.
SELECT count(*) AS view_count
FROM saved_views
WHERE tenant_id = $1 AND user_id = $2 AND surface = $3;

-- name: InsertSavedView :one
-- Persist a new saved view. The case-insensitive unique index
-- (tenant_id, user_id, surface, lower(name)) rejects a duplicate name with
-- a unique-violation the handler maps to 409. filters is the handler-
-- validated criteria payload (threat-model T).
INSERT INTO saved_views (
    tenant_id, user_id, surface, name, filters
) VALUES ($1, $2, $3, $4, $5)
RETURNING id, tenant_id, user_id, surface, name, filters, created_at, updated_at;

-- name: DeleteSavedView :one
-- Delete one of the caller's own views. The user_id predicate means a
-- caller can never delete another user's view even within the same tenant.
-- RETURNING lets the handler 404 when the id was not the caller's.
DELETE FROM saved_views
WHERE tenant_id = $1 AND user_id = $2 AND id = $3
RETURNING id;
