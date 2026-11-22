-- security-atlas -- #2910: policy_audit_log records the drifted page hash
-- on an in-place sync takeover.
--
--   sync_drift_body_sha256   hex sha256 of the page body, set on a
--                            'sync_takeover' row whose page body differs in
--                            content from the imported text it took over.
--
-- body_sha256 keeps its meaning on every action: the hash of the policy
-- row's own body, which an in-place takeover does not rewrite. The page's
-- hash goes in its own column so both are on the audit row.

ALTER TABLE policy_audit_log
    ADD COLUMN IF NOT EXISTS sync_drift_body_sha256 TEXT NULL
        CONSTRAINT policy_audit_log_sync_drift_body_sha256_chk
        CHECK (sync_drift_body_sha256 IS NULL
               OR (action = 'sync_takeover' AND sync_drift_body_sha256 ~ '^[0-9a-f]{64}$'));
