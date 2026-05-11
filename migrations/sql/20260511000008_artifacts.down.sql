-- Reverse of 20260511000008_artifacts.sql.
--
-- Drops the access log first (it has the FK back to artifacts), then
-- the artifacts table itself. Indexes / policies / constraints go with
-- the tables via CASCADE.

DROP TABLE IF EXISTS artifact_access_log CASCADE;
DROP TABLE IF EXISTS artifacts CASCADE;
