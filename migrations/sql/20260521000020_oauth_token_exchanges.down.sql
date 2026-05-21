-- Reverse of 20260521000020_oauth_token_exchanges.sql. Drops the
-- table + indexes + policies cleanly. Symmetric for byte-clean
-- up/down/up round-trip.

DROP POLICY IF EXISTS tenant_read ON oauth_token_exchanges;
DROP POLICY IF EXISTS tenant_write ON oauth_token_exchanges;
DROP INDEX IF EXISTS idx_oauth_token_exchanges_tenant_time;
DROP INDEX IF EXISTS idx_oauth_token_exchanges_jti;
DROP TABLE IF EXISTS oauth_token_exchanges;
