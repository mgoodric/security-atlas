-- Reverse of 20260612060000_control_owner_assign_saved_views.sql (slice 468).
-- Drop in reverse dependency order. control_owner_assignment_audit_log and
-- saved_views have no inbound FKs; control_owner_assignments references
-- controls (outbound) so it drops cleanly too.

DROP TABLE IF EXISTS saved_views CASCADE;
DROP TABLE IF EXISTS control_owner_assignment_audit_log CASCADE;
DROP TABLE IF EXISTS control_owner_assignments CASCADE;
