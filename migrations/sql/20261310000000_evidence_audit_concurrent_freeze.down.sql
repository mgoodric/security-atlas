-- security-atlas -- reverse 20261310000000_evidence_audit_concurrent_freeze.sql.
--
-- evidence_audit_log is append-only, so rows already audited as
-- rejected_concurrent_freeze stay. The narrower CHECK is added NOT VALID:
-- it binds new rows and leaves those in place.

ALTER TABLE evidence_audit_log
    DROP CONSTRAINT evidence_audit_log_decision_chk;

ALTER TABLE evidence_audit_log
    ADD CONSTRAINT evidence_audit_log_decision_chk
        CHECK (decision IN (
            'accepted',
            'deduplicated',
            'rejected_validation',
            'rejected_unknown_kind',
            'rejected_idempotency_mismatch',
            'rejected_scope_violation',
            'rejected_observed_at_skew',
            'rejected_oversized',
            'rejected_rate_limit',
            'rejected_unauthenticated',
            'rejected_internal_error'
        )) NOT VALID;
