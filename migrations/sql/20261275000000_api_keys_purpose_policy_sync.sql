-- security-atlas -- #2849: an API-key purpose for policy sync.
--
-- The Confluence policy connector (#2813) pushes policies with a key that
-- can do nothing else. Its api_keys row carries purpose = 'policy-sync';
-- the platform refuses such a key on every route except the policy sync
-- endpoint (none yet: that endpoint is #2850).
--
-- The CHECK keeps listing every allowed value, so an unknown purpose still
-- fails the INSERT instead of minting an unmarked key.

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_purpose_chk;

ALTER TABLE api_keys ADD CONSTRAINT api_keys_purpose_chk
    CHECK (purpose IS NULL OR purpose IN ('bootstrap-recovery', 'policy-sync'));

COMMENT ON COLUMN api_keys.purpose IS
    'Why the key exists, when it is not an ordinary issued key. '
    '''bootstrap-recovery'' marks the admin key reset-bootstrap minted (#1892). '
    '''policy-sync'' marks a key that may only sync policies (#2849).';
