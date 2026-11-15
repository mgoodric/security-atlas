-- Reverts 20261103000000_framework_scope_predicate_frozen.sql. The predicate
-- of an activated or superseded revision becomes editable in place again.
DROP TRIGGER IF EXISTS framework_scopes_predicate_frozen_trg ON framework_scopes;
DROP FUNCTION IF EXISTS framework_scopes_predicate_frozen();
