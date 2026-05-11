-- Reverse of 20260511000004_evidence_ledger.sql.
--
-- Restores the slice-002 RLS shape on evidence_records (single
-- tenant_isolation USING policy) and removes the slice-013 audit log
-- table + new columns + indexes.
--
-- NOTE: rolling back ingestion is intentionally destructive of rows
-- pushed via the SCF-anchor path (`scf:VPM-04` etc.), since the
-- restored slice-002 schema requires `control_id UUID NOT NULL` and
-- those rows carry NULL `control_id` + a free-form `control_ref`.
-- The append-only ledger semantics mean a rollback already accepts
-- ingest-time data loss; the DELETE below makes that loss explicit
-- and unblocks the NOT NULL restoration. If you need to preserve
-- those rows across a rollback, export them out of the audit log
-- before running this migration.

DROP TABLE IF EXISTS evidence_audit_log CASCADE;

DROP INDEX IF EXISTS evidence_records_tenant_idem_uniq;
DROP INDEX IF EXISTS idx_evidence_records_tenant_kind_observed;
DROP INDEX IF EXISTS idx_evidence_records_credential;

DROP POLICY IF EXISTS tenant_read ON evidence_records;
DROP POLICY IF EXISTS tenant_insert ON evidence_records;

-- Reinstate the slice-002 single policy so the round-trip leaves the
-- table in a byte-identical (semantically) state to the slice-002 baseline.
CREATE POLICY tenant_isolation ON evidence_records
    USING (current_tenant_matches(tenant_id));

ALTER TABLE evidence_records
    DROP CONSTRAINT IF EXISTS evidence_records_control_ref_nonempty,
    DROP CONSTRAINT IF EXISTS evidence_records_ingestion_path_chk;

ALTER TABLE evidence_records
    DROP COLUMN IF EXISTS control_ref,
    DROP COLUMN IF EXISTS source_attribution,
    DROP COLUMN IF EXISTS ingestion_path,
    DROP COLUMN IF EXISTS credential_id,
    DROP COLUMN IF EXISTS schema_version,
    DROP COLUMN IF EXISTS evidence_kind,
    DROP COLUMN IF EXISTS idempotency_key;

-- Drop rows that were ingested via the SCF-anchor path (control_id IS
-- NULL, control_ref carries the anchor string). The restored slice-002
-- schema requires control_id NOT NULL; these rows cannot survive the
-- rollback. See the header comment for the data-loss rationale.
DELETE FROM evidence_records WHERE control_id IS NULL;

-- Restore the slice-002 NOT NULL on control_id. Now safe because the
-- DELETE above removed every row that would have violated the constraint.
ALTER TABLE evidence_records
    ALTER COLUMN control_id SET NOT NULL;
