-- Reverse of 20261263000000_action_plan_system_move_nonce.sql (#2458).
-- Restores #2109's reconcile function and guard (owner role + GUC 'on')
-- and drops the nonce table, which never holds a committed row.

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

DROP TABLE IF EXISTS action_plan_system_move_nonces;
