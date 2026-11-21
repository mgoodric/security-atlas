-- Reverses 20261275000000_api_keys_purpose_policy_sync.sql: restores the
-- api_keys purpose CHECK of 20260814000000_api_keys_purpose.sql.
--
-- A policy-sync key would fail the narrower CHECK. Revoking or deleting
-- the keys is the operator's call, so this refuses while any exists.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM api_keys WHERE purpose = 'policy-sync';
    IF n > 0 THEN
        RAISE EXCEPTION 'api_keys holds % policy-sync key(s); refusing to narrow the purpose CHECK', n
            USING HINT = 'Remove those keys deliberately before reversing 20261275000000.';
    END IF;
END
$$;

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_purpose_chk;

ALTER TABLE api_keys ADD CONSTRAINT api_keys_purpose_chk
    CHECK (purpose IS NULL OR purpose IN ('bootstrap-recovery'));

COMMENT ON COLUMN api_keys.purpose IS
    'Why the key exists, when it is not an ordinary issued key. '
    '''bootstrap-recovery'' marks the admin key reset-bootstrap minted (#1892).';
