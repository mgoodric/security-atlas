-- Reverses 20261024000000_taxonomy_audit_log_themes.sql. The trail is
-- append-only, so theme rows cannot be removed; NOT VALID keeps them while
-- refusing new ones.
ALTER TABLE taxonomy_audit_log
    DROP CONSTRAINT taxonomy_audit_log_subject_kind_check;
ALTER TABLE taxonomy_audit_log
    ADD CONSTRAINT taxonomy_audit_log_subject_kind_check
    CHECK (subject_kind IN ('category')) NOT VALID;
