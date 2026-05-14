-- Reverse of 20260511000029_risk_control_links.sql.
--
-- Slice 020 only ADDed columns + CHECK constraints to the slice-019
-- `risk_control_links` table; it never created the table. So the down
-- migration drops exactly those four columns. Each DROP COLUMN drops the
-- column's CHECK constraint with it, so the constraints are not dropped
-- explicitly. The table, its PRIMARY KEY, composite FKs, indexes, and the
-- four-policy RLS split (all from slice 019's migration `_005`) are left
-- intact — reversing slice 020 must not unwind slice 019.
--
-- Round-trip safe: up -> down -> up is byte-clean. No enum types or sibling
-- objects were created by the up migration.

ALTER TABLE risk_control_links
    DROP COLUMN IF EXISTS design_score,
    DROP COLUMN IF EXISTS weight_design,
    DROP COLUMN IF EXISTS weight_operation,
    DROP COLUMN IF EXISTS weight_coverage;
