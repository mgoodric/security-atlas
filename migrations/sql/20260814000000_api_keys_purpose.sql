-- security-atlas -- #1892: mark API keys minted for bootstrap recovery.
--
-- POST /v1/admin/install/reset-bootstrap mints a tenant admin key and
-- writes its bearer to the bootstrap-token file. Two things revoke such a
-- key before its TTL: the next reset, and a first sign-in by a different
-- credential. Both find it by this marker plus its id, never by the bearer,
-- and the marker survives a restart (credstore reloads it at boot).
--
-- NULL is an ordinary key. The CHECK lists the only marker value so a typo
-- in application code fails the INSERT instead of minting an unmarked key.

ALTER TABLE api_keys ADD COLUMN purpose TEXT NULL;

ALTER TABLE api_keys ADD CONSTRAINT api_keys_purpose_chk
    CHECK (purpose IS NULL OR purpose IN ('bootstrap-recovery'));

COMMENT ON COLUMN api_keys.purpose IS
    'Why the key exists, when it is not an ordinary issued key. '
    '''bootstrap-recovery'' marks the admin key reset-bootstrap minted (#1892).';
