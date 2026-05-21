-- security-atlas — slice 191: down migration for oauth_device_codes.
--
-- Reverses 20260521000060_oauth_device_codes.sql. Device codes are
-- ephemeral by design (15-minute TTL); dropping the table forfeits
-- only in-flight device-authorization flows, which the operator
-- already accepts when running a down migration.

DROP TABLE IF EXISTS oauth_device_codes;
