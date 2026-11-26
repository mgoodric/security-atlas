-- Reverse #1737: drop the controls external_refs column.

ALTER TABLE controls DROP CONSTRAINT IF EXISTS controls_external_refs_array_chk;
ALTER TABLE controls DROP COLUMN IF EXISTS external_refs;
