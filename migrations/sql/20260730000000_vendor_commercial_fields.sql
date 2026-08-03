-- security-atlas — vendor commercial/software register extension (OE-623).
--
-- Extends the existing tenant-scoped vendor register instead of introducing
-- a parallel software primitive. Commercial fields are optional so the
-- risk-review workflow remains unchanged for vendors that are not tracked as
-- security tooling.

DO $$ BEGIN
    CREATE TYPE vendor_tool_category AS ENUM (
        'edr',
        'siem',
        'iam',
        'vuln_mgmt',
        'cloud_security',
        'appsec',
        'grc',
        'monitoring',
        'other'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE vendor_commercial_status AS ENUM (
        'active',
        'trialing',
        'churned'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE vendor_billing_cadence AS ENUM (
        'monthly',
        'quarterly',
        'annual',
        'multi_year',
        'one_time'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE vendors
    ADD COLUMN IF NOT EXISTS annual_cost DOUBLE PRECISION NULL,
    ADD COLUMN IF NOT EXISTS currency TEXT NULL,
    ADD COLUMN IF NOT EXISTS renewal_date DATE NULL,
    ADD COLUMN IF NOT EXISTS auto_renew BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS license_count INTEGER NULL,
    ADD COLUMN IF NOT EXISTS tool_category vendor_tool_category NULL,
    ADD COLUMN IF NOT EXISTS cost_owner TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_status vendor_commercial_status NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS billing_cadence vendor_billing_cadence NULL;

DO $$ BEGIN
    ALTER TABLE vendors
        ADD CONSTRAINT vendors_annual_cost_nonnegative
        CHECK (annual_cost IS NULL OR annual_cost >= 0);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE vendors
        ADD CONSTRAINT vendors_currency_iso_like
        CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE vendors
        ADD CONSTRAINT vendors_license_count_nonnegative
        CHECK (license_count IS NULL OR license_count >= 0);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE vendors
        ADD CONSTRAINT vendors_renewal_within_contract
        CHECK (
            (
                renewal_date IS NULL
                OR contract_start IS NULL
                OR renewal_date >= contract_start
            )
            AND (
                renewal_date IS NULL
                OR contract_end IS NULL
                OR renewal_date <= contract_end
            )
        );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE INDEX IF NOT EXISTS idx_vendors_tenant_tool_category
    ON vendors (tenant_id, tool_category)
    WHERE tool_category IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_vendors_tenant_renewal_date
    ON vendors (tenant_id, renewal_date)
    WHERE renewal_date IS NOT NULL;
