-- Reverses 20261312000000_action_plan_verify_ignores_replays.sql: restores
-- the 20261223000000 body, which reads the latest run including replays.

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
