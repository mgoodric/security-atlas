-- Reverse of 20260511000000_init.sql. Drops everything in dependency order
-- and leaves the public schema byte-identical to a fresh database.

DROP FUNCTION IF EXISTS current_tenant_matches(uuid);

DROP TABLE IF EXISTS framework_scopes CASCADE;
DROP TABLE IF EXISTS evidence_records CASCADE;
DROP TABLE IF EXISTS policies CASCADE;
DROP TABLE IF EXISTS scopes CASCADE;
DROP TABLE IF EXISTS risks CASCADE;
DROP TABLE IF EXISTS controls CASCADE;
DROP TABLE IF EXISTS framework_versions CASCADE;
DROP TABLE IF EXISTS frameworks CASCADE;

DROP TYPE IF EXISTS framework_scope_status;
DROP TYPE IF EXISTS policy_status;
DROP TYPE IF EXISTS framework_version_status;
DROP TYPE IF EXISTS evidence_freshness_class;
DROP TYPE IF EXISTS evidence_result;
DROP TYPE IF EXISTS scope_data_classification;
DROP TYPE IF EXISTS scope_environment;
DROP TYPE IF EXISTS risk_treatment;
DROP TYPE IF EXISTS risk_methodology;
DROP TYPE IF EXISTS risk_category;
DROP TYPE IF EXISTS control_lifecycle_state;
DROP TYPE IF EXISTS control_implementation_type;
