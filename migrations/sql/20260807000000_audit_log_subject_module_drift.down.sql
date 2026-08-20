-- Reverse OE-451 / PRIV-7: drop the subject_module retrofit columns from the
-- post-slice-180 audit-log-family tables. The original slice-180 nine-table
-- migration is intentionally untouched.

ALTER TABLE super_admin_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE imported_catalog_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE email_delivery_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE channel_delivery_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE csf_assessment_audit
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE staleness_rollup_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE scim_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE group_role_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE control_owner_assignment_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE action_plan_audit_log
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE framework_version_audit
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE questionnaire_mapping_proposal_audit
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE questionnaire_answer_reject_audit
    DROP COLUMN IF EXISTS subject_module;

ALTER TABLE change_audit_log
    DROP COLUMN IF EXISTS subject_module;
