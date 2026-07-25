-- Down migration for slice 440 — drop the board-narrative AI per-section table
-- for a clean up->down->up round-trip. DROP TABLE ... CASCADE removes the
-- table's RLS policies + indexes + constraints. The shared
-- ai_assist_human_approver_guard function is owned by slice 498 and is NOT
-- dropped here (other adopters depend on it). No TYPE was created.

DROP TABLE IF EXISTS board_narrative_sections CASCADE;
