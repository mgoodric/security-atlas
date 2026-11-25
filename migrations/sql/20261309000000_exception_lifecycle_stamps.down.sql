-- Reverses 20261309000000_exception_lifecycle_stamps.sql. The stamps the
-- up migration backfilled from the audit log are kept: they are true.

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_expired_has_stamp;

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_activated_has_stamp;
