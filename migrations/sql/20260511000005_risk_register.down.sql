-- Reverse of 20260511000005_risk_register.sql. Restores the slice-002 baseline
-- so the migration round-trip CI step (down-then-up) leaves a byte-identical DB.

DROP TABLE IF EXISTS risk_control_links CASCADE;

ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_tenant_id_unique;
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_accept_fields_required;
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_transfer_fields_required;

DROP INDEX IF EXISTS idx_risks_tenant_treatment;
DROP INDEX IF EXISTS idx_risks_tenant_category;
DROP INDEX IF EXISTS idx_risks_tenant_methodology;

ALTER TABLE risks DROP COLUMN IF EXISTS accepted_until;
ALTER TABLE risks DROP COLUMN IF EXISTS accepter;
ALTER TABLE risks DROP COLUMN IF EXISTS instrument_reference;

-- Restore the slice-002 USING-only policy.
DROP POLICY IF EXISTS tenant_read ON risks;
DROP POLICY IF EXISTS tenant_write ON risks;
DROP POLICY IF EXISTS tenant_update ON risks;
DROP POLICY IF EXISTS tenant_delete ON risks;

CREATE POLICY tenant_isolation ON risks
    USING (current_tenant_matches(tenant_id));
