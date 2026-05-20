-- Reverse slice 180: drop the `subject_module` column from each of the
-- nine platform audit-log tables. Restores the pre-slice-180 baseline.
--
-- Idempotent: `DROP COLUMN IF EXISTS` succeeds on re-apply against an
-- already-reverted database.
--
-- Reversibility note: this drops the column and the DEFAULT clause along
-- with it. Reapplying the forward migration restores both the column and
-- the DEFAULT, and existing rows backfill to `'core'` again via the DEFAULT.

ALTER TABLE decision_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE evidence_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE exception_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE sample_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE audit_period_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE aggregation_rule_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE feature_flag_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE me_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE walkthrough_audit_log
    DROP COLUMN IF EXISTS subject_module;
