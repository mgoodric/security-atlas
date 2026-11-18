-- Reverts 20261258000000_framework_scope_activated_at.sql. As-of reads fall
-- back to effective_from alone.
ALTER TABLE framework_scopes DROP CONSTRAINT IF EXISTS framework_scopes_activated_at_set;
DROP TRIGGER IF EXISTS framework_scopes_stamp_activated_at_trg ON framework_scopes;
DROP FUNCTION IF EXISTS framework_scopes_stamp_activated_at();
ALTER TABLE framework_scopes DROP COLUMN IF EXISTS activated_at;
