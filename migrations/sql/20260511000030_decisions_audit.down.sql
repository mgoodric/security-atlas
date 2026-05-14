-- Reverse of 20260511000030_decisions_audit.sql.
--
-- Slice 055 added one table (`decisions_audit`) and one column
-- (`decisions.audit_narrative_opt_out`). The down migration drops both in
-- FK-safe order. `decisions_audit` has no FK to `decisions`, so order
-- between the two statements does not matter, but the table is dropped
-- first by convention (children before parents).
--
-- DROP TABLE ... CASCADE removes the table's RLS policies and indexes
-- without per-object DROPs. DROP COLUMN drops the column's NOT NULL
-- DEFAULT with it.
--
-- Round-trip safe: up -> down -> up is byte-clean. No enum types or
-- sibling objects were created by the up migration.

DROP TABLE IF EXISTS decisions_audit CASCADE;

ALTER TABLE decisions
    DROP COLUMN IF EXISTS audit_narrative_opt_out;
