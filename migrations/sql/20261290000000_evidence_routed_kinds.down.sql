-- security-atlas -- reverse 20261290000000_evidence_routed_kinds.sql.
--
-- evidence_records is the append-only ledger, so this reversal refuses to
-- run while any row has an empty control_ref: the operator archives those
-- records deliberately first.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM evidence_records WHERE length(control_ref) = 0;
    IF n > 0 THEN
        RAISE EXCEPTION 'evidence_records holds % row(s) with an empty control_ref; refusing to delete ledger evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261290000000.';
    END IF;
END
$$;

ALTER TABLE evidence_records
    DROP CONSTRAINT evidence_records_control_ref_nonempty,
    ADD CONSTRAINT evidence_records_control_ref_nonempty
        CHECK (length(control_ref) > 0) NOT VALID;

ALTER TABLE evidence_records
    VALIDATE CONSTRAINT evidence_records_control_ref_nonempty;

DROP TABLE IF EXISTS evidence_routing_cutover;
