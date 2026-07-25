-- Reverse of 20260607000000_ai_generations.sql (slice 498).
--
-- The up migration added one table (`ai_generations`) and one function
-- (`ai_assist_human_approver_guard`). Drop both for a byte-clean
-- up -> down -> up round-trip (AC-5).
--
-- DROP TABLE ... CASCADE removes the table's RLS policies and indexes
-- without per-object DROPs. The function is dropped after the table; no
-- table in this migration references it (the reusable CHECK is shipped for
-- future adopters, not applied to ai_generations itself), so order is not
-- load-bearing, but the function is dropped last by convention.
--
-- No enum TYPE was created by the up migration, so none is dropped here.

DROP TABLE IF EXISTS ai_generations CASCADE;

DROP FUNCTION IF EXISTS ai_assist_human_approver_guard(BOOLEAN, BOOLEAN, TEXT);
