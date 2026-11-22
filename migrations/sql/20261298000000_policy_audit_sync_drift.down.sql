-- security-atlas -- reverse 20261298000000_policy_audit_sync_drift.sql.
--
-- policy_audit_log is append-only audit evidence, so this reversal refuses
-- to run while any row carries a drifted page hash: the operator archives
-- and removes them deliberately first.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM policy_audit_log
    WHERE sync_drift_body_sha256 IS NOT NULL;
    IF n > 0 THEN
        RAISE EXCEPTION 'policy_audit_log holds % row(s) with sync_drift_body_sha256; refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261298000000.';
    END IF;
END
$$;

ALTER TABLE policy_audit_log
    DROP COLUMN IF EXISTS sync_drift_body_sha256;
