-- security-atlas -- #2639: action_plan_audit_log.created_at orders by the
-- write, not the transaction start.
--
-- The verify owner rule (#2434, involvedSinceCompletion) reads the audit
-- trail in created_at order. With DEFAULT now() a row carries its
-- transaction's start time, so a PATCH that began before a completion and
-- waited on the plan row lock (GetActionPlanByIDForUpdate) until after it
-- committed sorts before the completion row. clock_timestamp() is read at
-- the INSERT, which every writer reaches only after the plan row lock, so
-- the trail sorts in commit order for writes to one plan.
--
-- Existing rows keep their timestamps. The rule itself also counts owners
-- on any row whose before- or after-status is completed or verified, so it
-- does not depend on order alone.
--
-- Reversible via 20261275000000_action_plan_audit_log_clock_timestamp.down.sql.

ALTER TABLE action_plan_audit_log
    ALTER COLUMN created_at SET DEFAULT clock_timestamp();
