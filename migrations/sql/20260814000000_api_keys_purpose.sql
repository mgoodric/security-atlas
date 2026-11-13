-- security-atlas -- #1892: mark API keys minted for bootstrap recovery, and
-- record who minted them.
--
-- POST /v1/admin/install/reset-bootstrap mints a tenant admin key and
-- writes its bearer to the bootstrap-token file. Two things revoke such a
-- key before its TTL: the next reset, and a first sign-in by a different
-- credential. Both find it by `purpose` plus its id, never by the bearer,
-- and the marker survives a restart (credstore reloads it at boot).
--
-- NULL is an ordinary key. The CHECK lists the only marker value so a typo
-- in application code fails the INSERT instead of minting an unmarked key.
--
-- `minted_by` is the user who minted the key, for attribution only. It is
-- deliberately not `issued_by`: the platform reads `issued_by` as the user
-- the key belongs to (credentialFromRow maps it to the key's UserID, which
-- /v1/me and approval records resolve, and the policy-acknowledgment
-- denominators count distinct issued_by over admin keys). A recovery key
-- must not act as, or count as a key of, the operator who minted it, so it
-- keeps issued_by NULL and records the minter here. Nothing reads
-- minted_by for identity or authorization.

ALTER TABLE api_keys ADD COLUMN purpose TEXT NULL;

ALTER TABLE api_keys ADD CONSTRAINT api_keys_purpose_chk
    CHECK (purpose IS NULL OR purpose IN ('bootstrap-recovery'));

ALTER TABLE api_keys ADD COLUMN minted_by UUID NULL;

COMMENT ON COLUMN api_keys.purpose IS
    'Why the key exists, when it is not an ordinary issued key. '
    '''bootstrap-recovery'' marks the admin key reset-bootstrap minted (#1892).';

COMMENT ON COLUMN api_keys.minted_by IS
    'users(id) of whoever minted the key, for attribution only (#1892). '
    'Not the key''s owner: that is issued_by. Never read for identity or authorization.';
