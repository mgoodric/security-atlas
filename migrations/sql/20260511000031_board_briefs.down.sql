-- Reverse of 20260511000031_board_briefs.sql.
--
-- Slice 031 added one table (`board_briefs`) and nothing else — no ALTER on a
-- pre-existing table, no enum types, no sibling objects. The down migration
-- is a single DROP TABLE.
--
-- DROP TABLE ... CASCADE removes the table's RLS policies and index without
-- per-object DROPs.
--
-- Round-trip safe: up -> down -> up is byte-clean.

DROP TABLE IF EXISTS board_briefs CASCADE;
