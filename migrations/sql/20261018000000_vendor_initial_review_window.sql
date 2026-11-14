-- security-atlas — #2061: a vendor that has never been reviewed is not overdue
-- the moment it is created.
--
-- Before this migration the vendor overdue rule treated a NULL
-- last_review_date as overdue, in Go (vendor.IsOverdueAsOf), in
-- ListOverdueVendors and in CountVendorsForBurndown. A vendor added a minute
-- ago showed as overdue.
--
-- The fix (decided in triage): a never-reviewed vendor has a first review due
-- on the UTC date of created_at plus an organisation initial-review window,
-- and is overdue only after that day. This column holds the window per tenant.
--
-- Why a column on `tenants`: same reasoning as slice 608's bundle_gate_mode
-- (20260608030000) — `tenants` is the per-tenant settings row, already under
-- FORCE RLS, so no new table or policy is needed (invariant #6). A tenant with
-- no `tenants` row (bare-UUID tenant predating slice 144) resolves to the same
-- 30-day default in both Go and SQL.
--
-- Existing tenants get 30 through the DEFAULT; no backfill.

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS vendor_initial_review_days INTEGER NOT NULL DEFAULT 30;

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_vendor_initial_review_days_chk;

ALTER TABLE tenants
    ADD CONSTRAINT tenants_vendor_initial_review_days_chk
    CHECK (vendor_initial_review_days BETWEEN 1 AND 365);

COMMENT ON COLUMN tenants.vendor_initial_review_days IS
    '#2061: days after a vendor''s creation (UTC date) that its first review is due. A never-reviewed vendor is overdue only after that day. Default 30.';
