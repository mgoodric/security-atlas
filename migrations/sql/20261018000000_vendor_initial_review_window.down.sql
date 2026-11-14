-- Reverses 20261018000000_vendor_initial_review_window.sql. Dropping the
-- column loses only a per-tenant setting, not evidence or audit history.

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_vendor_initial_review_days_chk;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS vendor_initial_review_days;
