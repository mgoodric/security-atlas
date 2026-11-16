-- Reverse of 20261223000000_action_plan_verify_from_evidence.sql (#2109).
-- The system moves stop: the reconcile function goes and the trigger
-- refuses verified -> completed again. action_plan_audit_log keeps
-- system_actor, its two CHECKs and a nullable actor_id: rows the system
-- wrote are the record, and the table is append-only.

DROP TRIGGER IF EXISTS action_plan_audit_log_system_actor_trg ON action_plan_audit_log;
DROP FUNCTION IF EXISTS action_plan_audit_log_system_actor_guard();

CREATE OR REPLACE FUNCTION action_plans_status_transition_guard()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF (OLD.status = 'draft'       AND NEW.status = 'in_progress')
    OR (OLD.status = 'in_progress' AND NEW.status IN ('blocked','completed'))
    OR (OLD.status = 'blocked'     AND NEW.status IN ('in_progress','completed'))
    OR (OLD.status = 'completed'   AND NEW.status IN ('verified','in_progress'))
    THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'illegal action_plan status transition: % -> %', OLD.status, NEW.status
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;

DROP FUNCTION IF EXISTS action_plan_in_system_move();
DROP FUNCTION IF EXISTS action_plans_reconcile_verification(UUID);
DROP FUNCTION IF EXISTS action_plan_linked_controls_pass(UUID, UUID);
