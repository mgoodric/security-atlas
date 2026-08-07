-- security-atlas - OE-451 / PRIV-7: reconcile slice-180 subject_module drift.
--
-- Slice 180 added `subject_module TEXT NOT NULL DEFAULT 'core'` to the nine
-- audit-log tables that existed in its explicit scope. The privacy audit in
-- slice 330 found later audit-log-family tables had drifted from that
-- pre-commitment. This migration extends the same idempotent shape to the
-- post-slice-180 audit-log-family tables verified at pickup time.
--
-- Deliberately NOT touched here: the three pre-slice-180 tables scoped out by
-- slice 180 (`artifact_access_log`, `decisions_audit`, `audit_sink_failures`).

ALTER TABLE super_admin_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE imported_catalog_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE email_delivery_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE channel_delivery_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE csf_assessment_audit
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE staleness_rollup_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE scim_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE group_role_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE control_owner_assignment_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE action_plan_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE framework_version_audit
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE questionnaire_mapping_proposal_audit
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE questionnaire_answer_reject_audit
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE change_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';
