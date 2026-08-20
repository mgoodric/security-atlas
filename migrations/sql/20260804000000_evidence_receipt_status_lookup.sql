-- security-atlas — OE-444: pusher-visible receipt terminal-status lookup.
--
-- The endpoint scopes by (tenant_id, credential_id, record_id) and returns the
-- newest audit decision for that receipt. Keep that read path indexed without
-- changing the append-only audit-log contract.

CREATE INDEX IF NOT EXISTS idx_evidence_audit_log_receipt_lookup
    ON evidence_audit_log (tenant_id, credential_id, record_id, received_at DESC)
    WHERE record_id IS NOT NULL;
