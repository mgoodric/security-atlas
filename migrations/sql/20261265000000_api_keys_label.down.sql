-- Reverse of 20261265000000_api_keys_label.sql (#2384).

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_label_chk;
ALTER TABLE api_keys DROP COLUMN IF EXISTS label;
