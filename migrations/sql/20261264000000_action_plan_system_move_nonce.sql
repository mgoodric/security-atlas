-- security-atlas -- #2458: the action-plan system-move guard checks a
-- per-call nonce, not just the owner role.
--
-- #2109's action_plan_in_system_move() was true for any code running as
-- the reconcile function's owner (atlas_migrate) while the GUC
-- atlas.action_plan_system_move was 'on'. The app role can set that GUC,
-- then call any other atlas_migrate-owned SECURITY DEFINER function (#2362,
-- #2364): inside it current_user is atlas_migrate, so the guard would pass.
-- None of those functions touches action_plans today; this closes the gap
-- before one does.
--
-- action_plans_reconcile_verification now draws a random nonce on each
-- move, writes it to action_plan_system_move_nonces and puts the same value
-- in the GUC. The guard passes only when the GUC names a row in that table.
-- The table has no grant to any role but its owner, so the app role can
-- neither write a nonce nor read one, and the function deletes its row
-- before it returns, so no nonce is ever committed or outlives the call.
-- A guessed GUC value names no row.
--
-- The owner check stays: it is what keeps a role without the table grant
-- from reading the table at all, and the guard checks it first.
--
-- See docs/audit-log/2458-action-plan-system-move-guard-decisions.md.
-- Reversible via 20261264000000_action_plan_system_move_nonce.down.sql.

-- No tenant_id: a nonce belongs to one call, not a tenant, and no role but
-- the owner can see the table. Rows live only inside the function.
CREATE TABLE IF NOT EXISTS action_plan_system_move_nonces (
    nonce UUID PRIMARY KEY
);

REVOKE ALL ON action_plan_system_move_nonces FROM PUBLIC;

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
    move_nonce UUID;
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
    --
    -- A control whose scope resolves to no cell still writes a run: one
    -- row with a NULL scope cell (eval.Engine), so the newest run decides,
    -- never an older one (#2458 D4).
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

        -- #2458: a fresh nonce per move. The guard matches the GUC against
        -- this row, which exists only between here and the DELETE below.
        move_nonce := gen_random_uuid();
        INSERT INTO public.action_plan_system_move_nonces (nonce) VALUES (move_nonce);
        PERFORM set_config('atlas.action_plan_system_move', move_nonce::text, true);

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
        DELETE FROM public.action_plan_system_move_nonces WHERE action_plan_system_move_nonces.nonce = move_nonce;
        moved := moved + 1;
    END LOOP;
    RETURN moved;
END;
$$;

-- True only inside action_plans_reconcile_verification: the caller runs as
-- the function's owner and the GUC names a nonce the function wrote.
-- plpgsql, not sql: the nonce lookup is planned only once the owner check
-- passes, so a caller without the table grant gets false, not 42501.
CREATE OR REPLACE FUNCTION action_plan_in_system_move()
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
SET search_path = public, pg_temp
AS $$
DECLARE
    marker TEXT := coalesce(current_setting('atlas.action_plan_system_move', true), '');
BEGIN
    IF marker = '' OR current_user <> (
        SELECT r.rolname
        FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner
        WHERE p.oid = 'action_plans_reconcile_verification(uuid)'::regprocedure)
    THEN
        RETURN false;
    END IF;
    RETURN EXISTS (
        SELECT 1 FROM public.action_plan_system_move_nonces n
        WHERE n.nonce::text = marker);
END;
$$;
