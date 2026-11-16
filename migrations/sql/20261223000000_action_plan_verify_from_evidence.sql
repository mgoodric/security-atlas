-- security-atlas -- #2109: action plans verified from evidence.
--
-- A completed plan whose linked controls all pass their latest evaluation
-- becomes verified; a verified plan with a linked control that now fails
-- returns to completed. Both moves are the system's, made after each
-- control evaluation, inside the evaluation's transaction, and logged
-- under a named system actor rather than a user.
--
-- 1. action_plan_audit_log gains system_actor. A row names a user
--    (actor_id) or the system (system_actor), never both, never neither.
--    Only the reconcile function below may write a system row.
--
-- 2. action_plan_linked_controls_pass(tenant, plan): NULL when the plan
--    has no linked control, true when every linked control's latest
--    evaluation run passed on every cell, false otherwise. A control with
--    no evaluation does not pass; `excepted` is not a pass (a waiver
--    closes no gap). The latest run is the control's most recent
--    eval_run_id, so a scope cell that no longer applies does not keep a
--    stale row in play.
--
-- 3. action_plans_reconcile_verification(control): the only path for the
--    two system moves. SECURITY DEFINER, pinned search_path, and it
--    refuses to run without a tenant context (the inverse of #2362's
--    guard: this is a tenant act). Every read and write filters on that
--    tenant explicitly, because the definer role bypasses RLS.
--
-- 4. action_plans_status_transition_guard allows verified -> completed
--    only when current_user is the reconcile function's owner and the
--    function has marked the transaction. atlas_app cannot become that
--    role: it can SET ROLE atlas_service_account, but not the owner, so a
--    SQL injection under the app role can set the marker GUC and still
--    not make the move. See docs/audit-log/2109-action-plan-verify-from-evidence-decisions.md.
--
-- Reversible via 20261223000000_action_plan_verify_from_evidence.down.sql.

-- ===== 1. system actor on the plan audit log =====

ALTER TABLE action_plan_audit_log
    ALTER COLUMN actor_id DROP NOT NULL;

-- IF NOT EXISTS / DROP IF EXISTS: the down migration keeps the column and
-- its rows (the log is the record), so a re-apply finds them in place.
ALTER TABLE action_plan_audit_log
    ADD COLUMN IF NOT EXISTS system_actor TEXT;

ALTER TABLE action_plan_audit_log
    DROP CONSTRAINT IF EXISTS action_plan_audit_log_one_actor,
    DROP CONSTRAINT IF EXISTS action_plan_audit_log_system_actor_known;

ALTER TABLE action_plan_audit_log
    ADD CONSTRAINT action_plan_audit_log_one_actor
        CHECK ((actor_id IS NULL) <> (system_actor IS NULL)),
    ADD CONSTRAINT action_plan_audit_log_system_actor_known
        CHECK (system_actor IS NULL OR system_actor = 'system:action-plan-verification');

-- ===== 2. do a plan's linked controls pass? =====

CREATE OR REPLACE FUNCTION action_plan_linked_controls_pass(p_tenant UUID, p_plan UUID)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
SET search_path = public, pg_temp
AS $$
    SELECT CASE WHEN count(*) = 0 THEN NULL ELSE bool_and(s.passes) END
    FROM (
        SELECT COALESCE((
            SELECT bool_and(ce.result = 'pass')
            FROM control_evaluations ce
            WHERE ce.tenant_id = p_tenant
              AND ce.control_id = apc.control_id
              AND ce.eval_run_id = (
                  SELECT l.eval_run_id
                  FROM control_evaluations l
                  WHERE l.tenant_id = p_tenant AND l.control_id = apc.control_id
                  ORDER BY l.evaluated_at DESC, l.created_at DESC
                  LIMIT 1)
        ), false) AS passes
        FROM action_plan_controls apc
        WHERE apc.tenant_id = p_tenant AND apc.action_plan_id = p_plan
    ) s
$$;

-- ===== 3. the system moves =====

CREATE OR REPLACE FUNCTION action_plans_reconcile_verification(p_control_id UUID)
RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    tenant UUID;
    r      RECORD;
    passes BOOLEAN;
    next   TEXT;
    moved  INTEGER := 0;
BEGIN
    tenant := NULLIF(current_setting('app.current_tenant', true), '')::uuid;
    IF tenant IS NULL THEN
        RAISE EXCEPTION 'action-plan reconcile: refused without a tenant context'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    -- Plans locked in id order, so two evaluations touching the same plans
    -- take their locks in the same order. Only action_plans rows are
    -- locked: no risk row, so the #2335 risk lock order is not in play.
    FOR r IN
        SELECT ap.id, ap.status
        FROM action_plans ap
        JOIN action_plan_controls apc
          ON apc.action_plan_id = ap.id AND apc.tenant_id = ap.tenant_id
        WHERE ap.tenant_id = tenant
          AND apc.control_id = p_control_id
          AND ap.tombstoned_at IS NULL
          AND ap.status IN ('completed', 'verified')
        ORDER BY ap.id
        FOR UPDATE OF ap
    LOOP
        passes := action_plan_linked_controls_pass(tenant, r.id);
        next := NULL;
        IF r.status = 'completed' AND passes IS TRUE THEN
            next := 'verified';
        ELSIF r.status = 'verified' AND passes IS NOT TRUE THEN
            next := 'completed';
        END IF;
        CONTINUE WHEN next IS NULL;

        PERFORM set_config('atlas.action_plan_system_move', 'on', true);
        UPDATE action_plans
        SET status = next, updated_at = now()
        WHERE tenant_id = tenant AND id = r.id;

        INSERT INTO action_plan_audit_log (
            tenant_id, action_plan_id, actor_id, system_actor,
            action_type, before_state, after_state)
        VALUES (
            tenant, r.id, NULL, 'system:action-plan-verification',
            'status_changed',
            jsonb_build_object('status', r.status),
            jsonb_build_object('status', next, 'control_id', p_control_id));
        PERFORM set_config('atlas.action_plan_system_move', '', true);
        moved := moved + 1;
    END LOOP;
    RETURN moved;
END;
$$;

REVOKE ALL ON FUNCTION action_plans_reconcile_verification(UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION action_plans_reconcile_verification(UUID) TO atlas_app;

-- ===== 4. the guards =====

-- True only inside action_plans_reconcile_verification: the caller runs as
-- the function's owner and the function has set the marker.
CREATE OR REPLACE FUNCTION action_plan_in_system_move()
RETURNS BOOLEAN
LANGUAGE sql
STABLE
SET search_path = public, pg_temp
AS $$
    SELECT coalesce(current_setting('atlas.action_plan_system_move', true), '') = 'on'
       AND current_user = (
           SELECT r.rolname
           FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner
           WHERE p.oid = 'action_plans_reconcile_verification(uuid)'::regprocedure)
$$;

CREATE OR REPLACE FUNCTION action_plans_status_transition_guard()
RETURNS TRIGGER AS $$
BEGIN
    -- Same status (or any non-status update): always allowed.
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF (OLD.status = 'draft'       AND NEW.status = 'in_progress')
    OR (OLD.status = 'in_progress' AND NEW.status IN ('blocked','completed'))
    OR (OLD.status = 'blocked'     AND NEW.status IN ('in_progress','completed'))
    OR (OLD.status = 'completed'   AND NEW.status IN ('verified','in_progress'))
    -- #2109: a linked control failing returns a verified plan to completed,
    -- and only the reconcile function makes that move.
    OR (OLD.status = 'verified'    AND NEW.status = 'completed' AND action_plan_in_system_move())
    THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'illegal action_plan status transition: % -> %', OLD.status, NEW.status
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;

-- A system-actor audit row is written by the reconcile function alone;
-- the app role's INSERT policy would otherwise let it forge one.
CREATE OR REPLACE FUNCTION action_plan_audit_log_system_actor_guard()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.system_actor IS NOT NULL AND NOT action_plan_in_system_move() THEN
        RAISE EXCEPTION 'action_plan_audit_log: a system-actor row is written only by the reconcile function'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER action_plan_audit_log_system_actor_trg
    BEFORE INSERT ON action_plan_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION action_plan_audit_log_system_actor_guard();
