-- security-atlas — slice 583 reverse migration.
--
-- Reverts the `user_notification_preferences_channel_check` CHECK constraint to
-- the slice-108 baseline by dropping 'slack' + 'webhook' from the admitted
-- channel set.
--
-- SAFETY: this TIGHTENS the CHECK. If any preference rows for the two slice-583
-- channels exist when this runs, the ADD CONSTRAINT would fail (Postgres
-- validates existing rows against the new tighter predicate). We therefore
-- DELETE those per-channel rows first — they are the per-kind opt-out CHOICES
-- this slice introduced, so removing them on rollback restores the pre-slice
-- state exactly (default-on for slack/webhook). The forward migration inserts
-- no rows, so a forward-rollback with no intervening user edit reverts cleanly;
-- the DELETE only fires when a user has set explicit slack/webhook per-kind
-- prefs after the forward migration ran.

DELETE FROM user_notification_preferences
    WHERE channel IN ('slack', 'webhook');

ALTER TABLE user_notification_preferences
    DROP CONSTRAINT IF EXISTS user_notification_preferences_channel_check;

ALTER TABLE user_notification_preferences
    ADD CONSTRAINT user_notification_preferences_channel_check
    CHECK (channel IN ('in_app', 'email'));
