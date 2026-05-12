-- Reverse of 20260511000010_audit_samples.sql.
--
-- Drop order is the reverse of creation: dependent tables first, base table
-- last. CASCADE is intentionally NOT used so a buggy down migration that
-- leaves orphans surfaces as an error rather than silently dropping
-- unrelated rows.

DROP TABLE IF EXISTS sample_audit_log;
DROP TABLE IF EXISTS sample_annotations;
DROP TABLE IF EXISTS sample_evidence;
DROP TABLE IF EXISTS samples;
DROP TABLE IF EXISTS populations;
