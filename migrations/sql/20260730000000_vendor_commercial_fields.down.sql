-- Reverse of 20260730000000_vendor_commercial_fields.sql.

DROP INDEX IF EXISTS idx_vendors_tenant_renewal_date;
DROP INDEX IF EXISTS idx_vendors_tenant_tool_category;

ALTER TABLE vendors
    DROP CONSTRAINT IF EXISTS vendors_renewal_within_contract,
    DROP CONSTRAINT IF EXISTS vendors_license_count_nonnegative,
    DROP CONSTRAINT IF EXISTS vendors_currency_iso_like,
    DROP CONSTRAINT IF EXISTS vendors_annual_cost_nonnegative;

ALTER TABLE vendors
    DROP COLUMN IF EXISTS billing_cadence,
    DROP COLUMN IF EXISTS commercial_status,
    DROP COLUMN IF EXISTS cost_owner,
    DROP COLUMN IF EXISTS tool_category,
    DROP COLUMN IF EXISTS license_count,
    DROP COLUMN IF EXISTS auto_renew,
    DROP COLUMN IF EXISTS renewal_date,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS annual_cost;

DROP TYPE IF EXISTS vendor_billing_cadence;
DROP TYPE IF EXISTS vendor_commercial_status;
DROP TYPE IF EXISTS vendor_tool_category;
