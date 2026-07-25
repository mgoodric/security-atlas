-- Down-migration for slice 474 — drop the canonical-scope column.
--
-- Reversible and safe: the column is additive and nullable, so dropping it
-- returns the ledger to its pre-slice-474 shape. After a down-migrate the
-- verify walk falls back to the slice-464 scope-free reconstruction for ALL
-- rows (the slice-464 baseline behavior). No evidence row content is lost —
-- only the verify-fidelity hint is removed. Never auto-run (P0-473-2:
-- migrate.sh skips *.down.sql).
ALTER TABLE evidence_records
    DROP COLUMN IF EXISTS scope_canonical;
