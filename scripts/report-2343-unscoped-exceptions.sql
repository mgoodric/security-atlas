-- security-atlas #2343 report: exceptions stored with no scope restriction.
--
-- A scope_cell_predicate of JSON null or {} matches every scope cell of the
-- control. POST /v1/exceptions has refused null since #2323, but rows
-- written before that, and every row filed with the field left out (stored
-- as {}), still carry one. This counts them so Matt can choose to leave
-- them, rewrite them to {"op":"true"}, or flag them for review.
--
-- Read-only. It runs in a READ ONLY transaction that is rolled back, and
-- prints tenant ids and counts only: never an exception id, a control, a
-- justification or any other text. Run it as a role that can see every
-- tenant (the migrate/owner role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/report-2343-unscoped-exceptions.sql
--
-- Nothing here changes a row.

BEGIN TRANSACTION READ ONLY;

-- 1. Per tenant and status: how many exceptions are unscoped, by spelling.
--    'active' rows are the ones the evaluator is waiving today.
SELECT tenant_id,
       status,
       count(*) FILTER (WHERE jsonb_typeof(scope_cell_predicate) = 'null') AS null_predicate,
       count(*) FILTER (WHERE scope_cell_predicate = '{}'::jsonb) AS empty_object,
       count(*) AS all_exceptions
FROM exceptions
GROUP BY tenant_id, status
HAVING count(*) FILTER (WHERE jsonb_typeof(scope_cell_predicate) = 'null'
                           OR scope_cell_predicate = '{}'::jsonb) > 0
ORDER BY tenant_id, status;

-- 2. Totals across tenants, so the per-tenant rows can be checked, with the
--    active ones on their own line.
SELECT CASE WHEN status = 'active' THEN 'active' ELSE 'not active' END AS bucket,
       count(*) FILTER (WHERE jsonb_typeof(scope_cell_predicate) = 'null') AS null_predicate,
       count(*) FILTER (WHERE scope_cell_predicate = '{}'::jsonb) AS empty_object,
       count(*) FILTER (WHERE scope_cell_predicate = '{"op":"true"}'::jsonb) AS explicit_true,
       count(*) AS all_exceptions,
       count(DISTINCT tenant_id) AS tenants
FROM exceptions
GROUP BY 1
ORDER BY 1;

ROLLBACK;
