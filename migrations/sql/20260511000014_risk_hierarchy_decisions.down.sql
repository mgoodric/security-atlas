-- Reverse of 20260511000014_risk_hierarchy_decisions.sql. Drops the slice-052
-- additions in FK-safe order so the round-trip (up → down → up) is byte-clean.
-- Run BEFORE 20260511000015_seed_default_themes.down.sql; the seed down assumes
-- org_themes still exists.
--
-- DROP TABLE ... CASCADE removes the per-table RLS policies, FK constraints,
-- and indexes without needing per-object DROPs. Composite UNIQUE on
-- framework_scopes is dropped explicitly because the table itself stays.

-- Link tables first (FK children of decisions + targets).
DROP TABLE IF EXISTS decision_scope_predicates CASCADE;
DROP TABLE IF EXISTS decision_exceptions      CASCADE;
DROP TABLE IF EXISTS decision_controls        CASCADE;
DROP TABLE IF EXISTS decision_risks           CASCADE;

-- Decisions.
DROP TABLE IF EXISTS decisions                CASCADE;

-- Risk aggregations (FK to risks).
DROP TABLE IF EXISTS risk_aggregations        CASCADE;

-- framework_scopes composite UNIQUE we added for the decision_scope_predicates FK.
ALTER TABLE framework_scopes
    DROP CONSTRAINT IF EXISTS framework_scopes_tenant_id_unique;

-- risks columns + FK + indexes added in slice 052.
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_org_unit_fk;
DROP INDEX IF EXISTS idx_risks_themes_gin;
DROP INDEX IF EXISTS idx_risks_tenant_org_unit;
DROP INDEX IF EXISTS idx_risks_tenant_level;
ALTER TABLE risks DROP COLUMN IF EXISTS themes;
ALTER TABLE risks DROP COLUMN IF EXISTS org_unit_id;
ALTER TABLE risks DROP COLUMN IF EXISTS level;

-- Theme catalog.
DROP TABLE IF EXISTS org_themes               CASCADE;

-- Org units (the self-ref FK is dropped by CASCADE).
DROP TABLE IF EXISTS org_units                CASCADE;

-- Enums last — must be after every column and table using them is gone.
DROP TYPE IF EXISTS decision_status;
DROP TYPE IF EXISTS risk_level;
