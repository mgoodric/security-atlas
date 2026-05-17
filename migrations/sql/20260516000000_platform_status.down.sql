-- security-atlas — platform_status singleton DOWN migration (slice 073).
--
-- Drops the table created by 20260516000000_platform_status.sql. RLS
-- policies and the GRANT are dropped automatically when the table is
-- dropped.

DROP TABLE IF EXISTS platform_status;
