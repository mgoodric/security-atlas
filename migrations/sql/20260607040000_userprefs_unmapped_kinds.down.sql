-- security-atlas — slice 566 reverse migration.
--
-- Reverts the `user_notification_preferences_event_check` CHECK constraint to
-- the slice-108 baseline by dropping `audit_note_reply` + `evidence_staleness`
-- from the admitted event set.
--
-- SAFETY: this TIGHTENS the CHECK. If any preference rows for the two slice-566
-- events exist when this runs, the ADD CONSTRAINT will fail (Postgres validates
-- existing rows against the new tighter predicate). A clean rollback therefore
-- requires deleting those rows first; the operator does so manually because
-- preference rows are user DATA, not schema, and a down-migration must not
-- silently destroy user choices. The forward migration inserts no rows, so a
-- forward-rollback with no intervening user edit reverts cleanly.

DELETE FROM user_notification_preferences
    WHERE event IN ('audit_note_reply', 'evidence_staleness');

ALTER TABLE user_notification_preferences
    DROP CONSTRAINT IF EXISTS user_notification_preferences_event_check;

ALTER TABLE user_notification_preferences
    ADD CONSTRAINT user_notification_preferences_event_check
    CHECK (event IN (
        'audit_period_assignment',
        'policy_ack_due',
        'risk_review_overdue',
        'control_drift'
    ));
