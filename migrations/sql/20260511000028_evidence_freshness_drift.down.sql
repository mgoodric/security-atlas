-- Reverse of 20260511000028_evidence_freshness_drift.sql.
--
-- Both tables are net-new in the up migration — no pre-existing table was
-- ALTERed, no enum type was created, no constraint was added to another
-- table. The down migration is therefore a simple two-statement DROP TABLE.
--
-- Drop order does not matter: neither table FKs to the other. Each table's
-- RLS policies, CHECK constraints, the composite FK on evidence_freshness,
-- and all indexes are dropped implicitly by DROP TABLE. CASCADE is
-- intentionally NOT used — a buggy down migration that left dependents would
-- surface as an error rather than silently dropping unrelated objects (same
-- convention as slices 011 / 026 / 027).
--
-- Round-trip safe: up -> down -> up is byte-clean.

DROP TABLE IF EXISTS control_drift_snapshots;

DROP TABLE IF EXISTS evidence_freshness;
