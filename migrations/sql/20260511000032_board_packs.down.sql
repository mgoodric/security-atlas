-- Reverse of 20260511000032_board_packs.sql.
--
-- Slice 032 added one table (`board_packs`), one trigger function, and one
-- trigger — no ALTER on a pre-existing table, no enum types, no sibling
-- objects.
--
-- DROP TABLE ... CASCADE removes the table's RLS policies, index, and the
-- attached trigger. The trigger FUNCTION is a standalone object and is
-- dropped explicitly afterward.
--
-- Round-trip safe: up -> down -> up is byte-clean.

DROP TABLE IF EXISTS board_packs CASCADE;
DROP FUNCTION IF EXISTS board_packs_block_published_update();
