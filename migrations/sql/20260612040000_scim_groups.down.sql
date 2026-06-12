-- Down migration for slice 733 — drop the SCIM /Groups backing store for a
-- clean up->down->up round-trip. scim_group_members is dropped first
-- (it FKs scim_groups), though the FK's ON DELETE CASCADE would also handle it.

DROP TABLE IF EXISTS scim_group_members;
DROP TABLE IF EXISTS scim_groups;
