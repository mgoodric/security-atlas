-- Reverse #2450: drop the vendor audit log.
--
-- Its rows are audit evidence, so the down migration refuses while any
-- exist, as 20261226000000's down refuses over 'vendor_owner_update' rows.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM vendor_audit_log;
    IF n > 0 THEN
        RAISE EXCEPTION 'vendor_audit_log holds % rows; refusing to drop audit evidence', n;
    END IF;
END $$;

DROP TABLE IF EXISTS vendor_audit_log;
DROP FUNCTION IF EXISTS vendor_audit_log_append_only();
