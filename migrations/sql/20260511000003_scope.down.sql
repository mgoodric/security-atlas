-- Reverse of 20260511000003_scope.sql. Drops scope_cells + scope_dimensions
-- and leaves the slice-002 baseline byte-identical.

DROP TABLE IF EXISTS scope_cells CASCADE;
DROP TABLE IF EXISTS scope_dimensions CASCADE;
