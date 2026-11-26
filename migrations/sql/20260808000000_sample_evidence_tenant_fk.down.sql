-- Reverse #1702: restore the single-column evidence FKs from
-- 20260511000010_audit_samples.sql. Runs before
-- 20260804000000_incident_register.down.sql drops the
-- evidence_records_tenant_id_unique constraint these FKs depend on.

ALTER TABLE sample_annotations
    DROP CONSTRAINT IF EXISTS sample_annotations_evidence_fk,
    ADD CONSTRAINT sample_annotations_evidence_fk
        FOREIGN KEY (evidence_record_id)
        REFERENCES evidence_records(id) ON DELETE RESTRICT;

ALTER TABLE sample_evidence
    DROP CONSTRAINT IF EXISTS sample_evidence_evidence_fk,
    ADD CONSTRAINT sample_evidence_evidence_fk
        FOREIGN KEY (evidence_record_id)
        REFERENCES evidence_records(id) ON DELETE RESTRICT;
