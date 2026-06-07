-- Reverse of 20260606010000_oscal_imported_catalogs.sql. Drops the three
-- new tables in topological order (children first). IF EXISTS keeps the
-- down idempotent. The bundled SCF spine (scf_anchors) is never touched.

DROP TABLE IF EXISTS imported_catalog_audit_log;
DROP TABLE IF EXISTS imported_catalog_controls;
DROP TABLE IF EXISTS imported_catalogs;
