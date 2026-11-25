-- security-atlas -- #3071: a replay row does not verify an action plan.
--
-- A replay of an earlier instant T (evaluated_as_of set, #3056) is stamped
-- evaluated_at = the clock of the run, so it sorted as each control's latest
-- run. action_plan_linked_controls_pass read that run, so replaying T could
-- move a plan completed <-> verified on how its controls stood at T, not
-- now. The run it reads is now the latest live run, as every other
-- latest-state read does (docs/audit-log/3071-replay-latest-state-decisions.md).
--
-- Body otherwise unchanged from 20261223000000.

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
                    AND l.evaluated_as_of IS NULL
                  ORDER BY l.evaluated_at DESC, l.created_at DESC
                  LIMIT 1)
        ), false) AS passes
        FROM action_plan_controls apc
        WHERE apc.tenant_id = p_tenant AND apc.action_plan_id = p_plan
    ) s
$$;
