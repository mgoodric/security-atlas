-- Reverse of 20260511000027_control_evaluations.sql.
--
-- Drop order: the control_evaluations table first (it FKs to scope_cells via
-- the constraint added in the up migration), then the scope_cells composite
-- UNIQUE constraint. CASCADE is intentionally NOT used so a buggy down
-- migration that leaves orphans surfaces as an error rather than silently
-- dropping unrelated objects — same convention as slices 011 / 026.
--
-- The table's RLS policies, CHECK constraints, FKs, and indexes are dropped
-- implicitly by DROP TABLE. The scope_cells constraint is dropped explicitly
-- because the scope_cells table itself stays.
--
-- Round-trip safe: up -> down -> up is byte-clean. No enum types were created
-- by the up migration (result reuses slice-002's evidence_result), so there
-- is nothing to DROP TYPE here.

DROP TABLE IF EXISTS control_evaluations;

ALTER TABLE scope_cells
    DROP CONSTRAINT IF EXISTS scope_cells_tenant_id_unique;
