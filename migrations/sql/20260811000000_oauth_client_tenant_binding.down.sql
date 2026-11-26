-- Reverses 20260811000000_oauth_client_tenant_binding.sql: clients are
-- tenant-free again.

ALTER TABLE oauth_clients DROP COLUMN IF EXISTS bound_tenant_id;
