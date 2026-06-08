-- security-atlas — slice 583: per-kind opt-out for the Slack + webhook channels.
--
-- Slice 542 layered the slice-108 per-event `email` channel on top of the
-- slice-445 master email opt-in. Slice 543 shipped Slack + webhook with the
-- MASTER opt-in only (per-kind deferred). This slice generalizes the slice-542
-- per-kind filter to those two channels. The slice-108
-- `user_notification_preferences.channel` CHECK constraint admits only
-- ('in_app', 'email'); this migration widens it to admit ('slack', 'webhook')
-- so a user can opt out of an individual notification kind for either channel.
--
-- WHITELIST-MOVE-TOGETHER DISCIPLINE (slice 108): the schema CHECK and the Go
-- whitelist (internal/auth/userprefs.Channels) must extend in the SAME slice.
-- This migration is the schema half; the Go half lands in the same PR. A value
-- in one but not the other is a latent 500.
--
-- Backward-compatible default (slice 583, inheriting slice-542 D2 /
-- slice-108 D3): this migration ONLY widens the admitted channel set — it
-- inserts NO rows. A user with no row for a (event, slack|webhook) cell still
-- reads default-on: they keep receiving every kind on a channel they have
-- opted into (master) until they set an explicit per-kind enabled=false. No
-- silent suppression.
--
-- The CHECK widening is monotonic, so this is safe to apply over existing data
-- (no existing row can violate the wider predicate).
--
-- Reversible via 20260608020000_userprefs_slack_webhook_channels.down.sql.
-- NOTE: the down migration narrows the CHECK back to ('in_app', 'email'); it
-- first DELETEs any slack/webhook rows so the narrower predicate can be
-- re-applied without violating existing data (documented in the .down file).

ALTER TABLE user_notification_preferences
    DROP CONSTRAINT IF EXISTS user_notification_preferences_channel_check;

ALTER TABLE user_notification_preferences
    ADD CONSTRAINT user_notification_preferences_channel_check
    CHECK (channel IN ('in_app', 'email', 'slack', 'webhook'));
