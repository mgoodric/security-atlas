-- Reverse migration for slice 035.
-- Drop in dependency order: decision_audit_log first (no FKs into it),
-- then user_roles.

DROP TABLE IF EXISTS decision_audit_log;
DROP TABLE IF EXISTS user_roles;
