-- Down migration for slice 471 — drop the role-scoped checklist tables for a
-- clean up->down->up round-trip. Children first (checklist_items FKs
-- checklist_sections). The shared ai_assist_human_approver_guard function is
-- owned by slice 498 and is NOT dropped here (other adopters depend on it).

DROP TABLE IF EXISTS checklist_items;
DROP TABLE IF EXISTS checklist_sections;
