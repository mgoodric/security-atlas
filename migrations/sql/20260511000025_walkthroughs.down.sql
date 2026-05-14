-- security-atlas — Slice 027 walkthroughs down migration.
--
-- Drops the three slice-027 tables in dependency order:
--   walkthrough_audit_log     - no inbound FKs; safe first
--   walkthrough_attachments   - FK -> walkthroughs(tenant_id, id)
--   walkthroughs              - FK -> controls + audit_periods (RESTRICT;
--                               drop after children are gone)
--
-- The audit_periods + controls + audit_notes tables are unchanged --
-- slice 029's audit_notes scope_type 'walkthrough' enum value remains
-- legal post-rollback (it's just an enum widening, not a column
-- introduction). Any audit_notes rows that reference walkthrough_ids
-- by scope_id will become dangling strings but the column is a free-
-- form text scope_id so no FK violation occurs.

REVOKE SELECT, INSERT ON walkthrough_audit_log FROM atlas_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON walkthrough_attachments FROM atlas_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON walkthroughs FROM atlas_app;

DROP TABLE IF EXISTS walkthrough_audit_log;
DROP TABLE IF EXISTS walkthrough_attachments;
DROP TABLE IF EXISTS walkthroughs;
