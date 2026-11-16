-- Reverse #2142: drop the per-user theme preference column.
ALTER TABLE users DROP COLUMN IF EXISTS theme;
