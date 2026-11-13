-- Reverse #1892: drop the api_keys purpose marker and minted_by.

ALTER TABLE api_keys DROP COLUMN IF EXISTS minted_by;
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_purpose_chk;
ALTER TABLE api_keys DROP COLUMN IF EXISTS purpose;
