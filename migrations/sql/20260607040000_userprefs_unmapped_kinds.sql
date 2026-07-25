-- security-atlas — slice 566: per-kind email opt-out for the two slice-445
-- digest kinds that previously had no slice-108 event row.
--
-- Slice 542 layered the slice-108 per-event `email` channel on top of the
-- slice-445 master email opt-in. But two notification kinds the digest renders
-- — `audit_note.reply` and `evidence.staleness` — had NO slice-108 event row,
-- so they were UNMAPPED (default-on, no per-kind opt-out surface). This
-- migration extends the `user_notification_preferences_event_check` CHECK
-- constraint to admit their event keys (`audit_note_reply`,
-- `evidence_staleness`), so a user can now opt out of either individually.
--
-- WHITELIST-MOVE-TOGETHER DISCIPLINE (slice 108): the schema CHECK and the
-- Go whitelist (internal/auth/userprefs.Events) must extend in the SAME slice.
-- This migration is the schema half; the Go half lands in the same PR. A value
-- in one but not the other is a latent 500.
--
-- Backward-compatible default (P0-566-1): this migration ONLY widens the
-- admitted event set — it inserts NO rows. A user with no row for the new
-- events still reads default-on (slice-108 D3 / slice-542 D2): they keep
-- receiving both kinds until they set an explicit per-kind email=false. No
-- silent suppression.
--
-- The CHECK widening is monotonic, so this is safe to apply over existing data
-- (no existing row can violate the wider predicate).
--
-- Reversible via 20260607040000_userprefs_unmapped_kinds.down.sql.

ALTER TABLE user_notification_preferences
    DROP CONSTRAINT IF EXISTS user_notification_preferences_event_check;

ALTER TABLE user_notification_preferences
    ADD CONSTRAINT user_notification_preferences_event_check
    CHECK (event IN (
        'audit_period_assignment',
        'policy_ack_due',
        'risk_review_overdue',
        'control_drift',
        'audit_note_reply',
        'evidence_staleness'
    ));
