-- Reverse of 20260612070000_action_plans.sql (slice 384).
-- Drop in reverse dependency order: the M2M + audit children first (they FK
-- the parent), then the parent, then the standalone functions. CASCADE on
-- the table drops removes their triggers + policies.

DROP TABLE IF EXISTS action_plan_audit_log CASCADE;
DROP TABLE IF EXISTS action_plan_controls CASCADE;
DROP TABLE IF EXISTS action_plan_risks CASCADE;
DROP TABLE IF EXISTS action_plans CASCADE;

DROP FUNCTION IF EXISTS action_plan_audit_log_append_only();
DROP FUNCTION IF EXISTS action_plans_status_transition_guard();
