-- Reverse of 20260521000010_oauth_clients.sql. Drops the
-- oauth_clients table and the supporting index. Symmetric for
-- byte-clean up/down/up round-trip.

DROP INDEX IF EXISTS idx_oauth_clients_active_lookup;
DROP TABLE IF EXISTS oauth_clients;
