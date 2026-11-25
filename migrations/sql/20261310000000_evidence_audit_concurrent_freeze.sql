-- security-atlas -- #3060: a distinct evidence_audit_log decision for a
-- write refused because an audit period froze while its transaction was
-- open (ingest.ErrConcurrentFreeze, #3038).
--
-- Before this, the refusal was audited as rejected_internal_error, which
-- reads as a server fault. It is not one: nothing was written, and the
-- caller retries. Process retries on its own; a domain writer
-- (ProcessInTx) answers 409 concurrent_freeze and the client retries.

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
            'rejected_concurrent_freeze',
            'rejected_internal_error'
        )) NOT VALID;

-- NOT VALID: the new set is the old one plus one token, so every existing
-- row already satisfies it. Validating would only scan the whole audit log
-- under the ACCESS EXCLUSIVE lock this ALTER holds (to commit, under
-- psql --single-transaction), stalling every ingest, to prove nothing. New
-- rows are checked either way. A later VALIDATE CONSTRAINT, outside this
-- migration, takes only SHARE UPDATE EXCLUSIVE if an operator wants the
-- constraint marked validated.
