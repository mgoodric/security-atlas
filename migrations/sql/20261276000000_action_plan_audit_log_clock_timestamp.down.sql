-- Reverses 20261276000000_action_plan_audit_log_clock_timestamp.sql (#2639).
ALTER TABLE action_plan_audit_log
    ALTER COLUMN created_at SET DEFAULT now();
