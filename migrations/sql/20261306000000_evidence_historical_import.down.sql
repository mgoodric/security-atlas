-- security-atlas -- reverse 20261306000000_evidence_historical_import.sql.
--
-- evidence_records and both audit logs are append-only, so imported
-- records and period_amended_by_import rows stay. The CHECK restores
-- refuse while any exist, which is the intended stop: rolling back would
-- otherwise drop the record of an amended freeze.

ALTER TABLE audit_period_audit_log
    DROP CONSTRAINT audit_period_audit_log_action_chk;

ALTER TABLE audit_period_audit_log
    ADD CONSTRAINT audit_period_audit_log_action_chk
    CHECK (action IN (
        'period_created',
        'period_frozen',
        'freeze_rejected_already_frozen',
        'population_attached',
        'auditor_assigned',
        'auditor_removed'
    ));

DROP TABLE IF EXISTS audit_period_freeze_revisions;

DROP INDEX IF EXISTS idx_evidence_audit_log_tenant_import_batch;

ALTER TABLE evidence_audit_log
    DROP COLUMN IF EXISTS import_batch_id;

ALTER TABLE evidence_records
    DROP CONSTRAINT evidence_records_ingestion_path_chk;

ALTER TABLE evidence_records
    ADD CONSTRAINT evidence_records_ingestion_path_chk
        CHECK (ingestion_path IN ('push', 'pull', 'subscribe', 'manual_upload', 'webhook'));
