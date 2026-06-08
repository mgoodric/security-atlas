-- Down-migration for slice 633 — drop the lossless observed-at column.
--
-- Reversible and safe: the column is additive and nullable, so dropping it
-- returns the ledger to its pre-slice-633 shape. After a down-migrate the
-- verify walk falls back to reconstructing observed_at from the lossy
-- `observed_at` TIMESTAMPTZ column for ALL rows (the slice-464/474 baseline).
-- No evidence row content is lost — only the verify-fidelity hint is removed.
-- Never auto-run (P0-473-2: migrate.sh skips *.down.sql).
ALTER TABLE evidence_records
    DROP COLUMN IF EXISTS observed_at_nanos;
