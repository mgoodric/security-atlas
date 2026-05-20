-- Slice 108 — /v1/me/* preferences + audit-log queries.
--
-- Queries land in their own file (not appended to users.sql or sessions.sql) so the
-- sqlc regen diff for slice 108 is scoped to one new dbx file: control over which
-- existing dbx files mutate per slice 109's regen reset.

-- name: ListUserNotificationPreferences :many
-- Read every preference row for (tenant, user). The application layer fills in defaults
-- (enabled=true) for any (event, channel) tuple absent from the result set. RLS enforces
-- the tenant scope; the explicit tenant_id filter in WHERE is defense-in-depth and lets
-- a future RLS misconfiguration still filter by tenant.
SELECT *
FROM user_notification_preferences
WHERE tenant_id = $1 AND user_id = $2
ORDER BY event ASC, channel ASC;

-- name: UpsertUserNotificationPreference :exec
-- Atomic per-cell upsert. The (tenant_id, user_id, event, channel) primary key is the
-- conflict target. On conflict we update enabled + updated_at. This is the natural
-- partial-merge primitive for AC-5 (PATCH /v1/me/preferences merges, no replacement).
INSERT INTO user_notification_preferences (
    tenant_id, user_id, event, channel, enabled, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, now()
)
ON CONFLICT (tenant_id, user_id, event, channel)
DO UPDATE SET
    enabled = EXCLUDED.enabled,
    updated_at = now();

-- name: InsertMeAuditLog :exec
-- Append a row to the slice 108 audit ledger. before / after are JSONB; the handler
-- builds them from the wire shape minus any redacted fields. Gated by handler logic on
-- non-empty diff (anti-criterion ISC-A5).
--
-- Slice 180: explicit `subject_module='core'` (column defaults to 'core' at
-- the DB layer; explicit-is-clearer per AC-5).
INSERT INTO me_audit_log (
    tenant_id, user_id, action, before, after, subject_module
)
VALUES (
    $1, $2, $3, $4, $5, 'core'
);
