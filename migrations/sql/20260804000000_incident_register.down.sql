DROP TABLE IF EXISTS incident_timeline;
DROP TABLE IF EXISTS incident_evidence_links;
DROP TABLE IF EXISTS incident_vendors;
DROP TABLE IF EXISTS incident_risks;
DROP TABLE IF EXISTS incident_controls;
DROP TABLE IF EXISTS incidents;

ALTER TABLE evidence_records
    DROP CONSTRAINT IF EXISTS evidence_records_tenant_id_unique;
