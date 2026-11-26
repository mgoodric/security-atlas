-- security-atlas — #1811: organisation theme management (#1745).
--
-- Theme create, rename and delete write to the taxonomy_audit_log #1810
-- added, as subject_kind 'theme'. The table's header anticipated this:
-- the theme ticket widens the CHECK rather than adding a second table.
-- subject_slug carries the theme name, which is what a theme is known by.

ALTER TABLE taxonomy_audit_log
    DROP CONSTRAINT taxonomy_audit_log_subject_kind_check;
ALTER TABLE taxonomy_audit_log
    ADD CONSTRAINT taxonomy_audit_log_subject_kind_check
    CHECK (subject_kind IN ('category', 'theme'));
