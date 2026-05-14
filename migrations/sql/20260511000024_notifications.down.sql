-- security-atlas -- reverse slice 029 notifications migration.
DROP INDEX IF EXISTS idx_notifications_recipient_unread_first;
DROP TABLE IF EXISTS notifications;
