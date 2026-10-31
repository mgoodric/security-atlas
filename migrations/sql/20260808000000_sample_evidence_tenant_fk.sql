-- security-atlas -- #1702: tenant-composite evidence FKs on the audit-sample
-- tables (C6).
--
-- sample_evidence and sample_annotations referenced evidence_records(id)
-- through single-column foreign keys. Postgres runs FK checks without RLS,
-- so a tenant-A session could insert a row pointing at tenant B's evidence
-- id: an existence oracle on B's ids and a dangling cross-tenant link.
--
-- This migration supersedes the note in 20260511000010_audit_samples.sql
-- that a composite key was impossible because evidence_records exposed no
-- UNIQUE (tenant_id, id). 20260804000000_incident_register.sql added
-- evidence_records_tenant_id_unique, so both FKs now become
-- (tenant_id, evidence_record_id) -> evidence_records(tenant_id, id).
-- That earlier migration is append-only and is not edited.
--
-- Pre-check: an existing cross-tenant row would make ADD CONSTRAINT fail
-- with a bare 23503. The DO block fails first, with the counts, and never
-- repairs or deletes rows: an operator decides what a cross-tenant link
-- means before the constraint can land. The migration role is BYPASSRLS,
-- so the counts see every tenant.

DO $$
DECLARE
    n_evidence    BIGINT;
    n_annotations BIGINT;
BEGIN
    SELECT count(*) INTO n_evidence
      FROM sample_evidence se
      JOIN evidence_records er ON er.id = se.evidence_record_id
     WHERE er.tenant_id <> se.tenant_id;

    SELECT count(*) INTO n_annotations
      FROM sample_annotations sa
      JOIN evidence_records er ON er.id = sa.evidence_record_id
     WHERE er.tenant_id <> sa.tenant_id;

    IF n_evidence > 0 OR n_annotations > 0 THEN
        RAISE EXCEPTION 'cross-tenant evidence links exist: sample_evidence=%, sample_annotations=%; resolve them before applying this migration',
            n_evidence, n_annotations
            USING ERRCODE = 'check_violation';
    END IF;
END $$;

ALTER TABLE sample_evidence
    DROP CONSTRAINT IF EXISTS sample_evidence_evidence_fk,
    ADD CONSTRAINT sample_evidence_evidence_fk
        FOREIGN KEY (tenant_id, evidence_record_id)
        REFERENCES evidence_records(tenant_id, id) ON DELETE RESTRICT;

ALTER TABLE sample_annotations
    DROP CONSTRAINT IF EXISTS sample_annotations_evidence_fk,
    ADD CONSTRAINT sample_annotations_evidence_fk
        FOREIGN KEY (tenant_id, evidence_record_id)
        REFERENCES evidence_records(tenant_id, id) ON DELETE RESTRICT;
