-- Slice 029 -- notifications spine.
--
-- In-app notifications. Slice 029 dispatches a row per distinct prior
-- thread author when a new audit_note lands; the recipient sees it on
-- /v1/me/notifications. Future slices may reuse this spine for other
-- notification types (evidence freshness, policy acknowledgment, etc.).
--
-- All queries are tenant-scoped via the leading (tenant_id, ...); RLS
-- under FORCE keeps the cross-tenant boundary safe even on a
-- misconfigured query. The recipient_user_id filter is the second-line
-- protection -- a caller cannot list another user's notifications.

-- name: CreateNotification :one
-- Insert a single notification row. The payload is opaque JSONB; the
-- handler/dispatch layer is responsible for the shape consistency per
-- notification type.
INSERT INTO notifications (
    id, tenant_id, recipient_user_id, type, payload, created_at
)
VALUES ($1, $2, $3, $4, $5, now())
RETURNING *;

-- name: ListNotificationsForUser :many
-- Recipient-scoped list. Unread first (NULLS FIRST on read_at), then
-- newest first within each read-status bucket. The index
-- idx_notifications_recipient_unread_first directly serves this order.
-- $3 is the limit and $4 the offset for paging.
SELECT * FROM notifications
WHERE tenant_id         = $1
  AND recipient_user_id = $2
ORDER BY read_at NULLS FIRST, created_at DESC, id ASC
LIMIT $3 OFFSET $4;

-- name: CountUnreadNotificationsForUser :one
-- Used by the /v1/me/notifications response to surface the unread count
-- in the page header.
SELECT COUNT(*) AS unread_count
FROM notifications
WHERE tenant_id         = $1
  AND recipient_user_id = $2
  AND read_at IS NULL;

-- name: MarkNotificationRead :one
-- Recipient-scoped mark-read. Idempotent: re-marking an already-read
-- notification preserves the original read_at (COALESCE keeps the
-- earliest timestamp).
UPDATE notifications
SET read_at = COALESCE(read_at, now())
WHERE tenant_id         = $1
  AND recipient_user_id = $2
  AND id                = $3
RETURNING *;
