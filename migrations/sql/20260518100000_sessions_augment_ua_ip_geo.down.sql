-- Reverse of 20260518100000_sessions_augment_ua_ip_geo.sql.
--
-- DROP COLUMN IF EXISTS on each column so the down migration is
-- idempotent even when only a subset of the forward migration
-- previously applied (e.g. partial failure).

ALTER TABLE sessions
    DROP COLUMN IF EXISTS geo_city,
    DROP COLUMN IF EXISTS geo_country,
    DROP COLUMN IF EXISTS ip_address,
    DROP COLUMN IF EXISTS user_agent;
