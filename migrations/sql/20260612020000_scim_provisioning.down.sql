-- Down migration for slice 508 — drop the SCIM provisioning surface for a
-- clean up->down->up round-trip.

DROP TABLE IF EXISTS scim_audit_log;
DROP TABLE IF EXISTS scim_credentials;

DROP INDEX IF EXISTS users_scim_external_id_per_tenant_unique;
ALTER TABLE users DROP COLUMN IF EXISTS scim_managed;
ALTER TABLE users DROP COLUMN IF EXISTS scim_external_id;
ALTER TABLE users DROP COLUMN IF EXISTS active;
