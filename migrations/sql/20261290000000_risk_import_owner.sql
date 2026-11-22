-- security-atlas — #2843: owners on staged risk import rows (spec #2812,
-- Q15).
--
-- risk_import_rows gains the owner approval creates the risk with:
--
--   owner_user_id   the active user of the tenant the row's owner cell
--                   matched by email, ignoring case. It becomes the risk's
--                   linked owner (risks.owner_user_id);
--   owner_unlinked  the owner cell when it matched no such user. It becomes
--                   the risk's free-text owner, and the row carries the
--                   owner_unlinked flag.
--
-- At most one of the two is set. Rows staged before this migration had no
-- owner column and were created without an owner, which the defaults keep.
-- No foreign key: a user removed after upload leaves the row approvable,
-- created without a linked owner, as the import's other staged references
-- (linked_control_ids) do.

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS owner_user_id  UUID NULL,
    ADD COLUMN IF NOT EXISTS owner_unlinked TEXT NOT NULL DEFAULT '';

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_owner_chk;
ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_owner_chk
        CHECK (owner_user_id IS NULL OR owner_unlinked = '');
