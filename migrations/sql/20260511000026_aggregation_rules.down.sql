-- Reverse of 20260511000026_aggregation_rules.sql.
--
-- Drop order: the two log tables first (they FK to aggregation_rules), then
-- aggregation_rules, then the standalone risks performance index. CASCADE
-- is intentionally NOT used on the tables so a buggy down migration that
-- leaves orphans surfaces as an error rather than silently dropping
-- unrelated objects — same convention as slice 011's down migration.
--
-- Each table's RLS policies, CHECK constraints, FKs, and indexes are
-- dropped implicitly by DROP TABLE. The risks performance index is dropped
-- explicitly because the `risks` table itself stays.
--
-- Round-trip safe: up -> down -> up is byte-clean. No enum types were
-- created by the up migration (parent_level reused slice 052's risk_level),
-- so there is nothing to DROP TYPE here.

DROP TABLE IF EXISTS aggregation_rule_audit_log;
DROP TABLE IF EXISTS aggregation_rule_evaluations;
DROP TABLE IF EXISTS aggregation_rules;

DROP INDEX IF EXISTS idx_risks_tenant_created_themes;
