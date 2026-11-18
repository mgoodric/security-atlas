-- security-atlas — #2358: stock controls the boot seed held back.
--
-- Bootstrap re-uploads every shipped controls/soc2/ manifest on each boot.
-- When a person approved an SCF re-anchor of that stock control (#1776),
-- or edited the control after approving one, the upload is kept as a
-- no-op so the reviewed anchor is not undone. A later shipped update to
-- that control is held back the same way, on every boot, and the tenant
-- never receives it. This table records each hold so an admin can list
-- them (GET /v1/admin/controls/held-back): one row per tenant and bundle,
-- naming the approved revision that caused the hold and the shipped
-- manifest that was not applied. The boot seed's upload upserts it.
--
-- The row is derived state, not a record of a decision, so it cascades
-- with the controls it names.

CREATE TABLE IF NOT EXISTS stock_control_holdbacks (
    tenant_id             UUID NOT NULL,
    bundle_id             TEXT NOT NULL,
    -- The approved remap revision (scf_remap_decisions.new_control_id) in
    -- the active control's lineage that the hold protects.
    approved_control_id   UUID NOT NULL,
    -- sha256 of the shipped control.yaml the boot seed did not apply.
    shipped_manifest_hash TEXT NOT NULL CHECK (shipped_manifest_hash ~ '^[0-9a-f]{64}$'),
    -- When this shipped manifest was first and most recently held back.
    first_held_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_held_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, bundle_id),
    CONSTRAINT stock_control_holdbacks_approved_control_fk
        FOREIGN KEY (tenant_id, approved_control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_stock_control_holdbacks_approved_control
    ON stock_control_holdbacks (tenant_id, approved_control_id);

ALTER TABLE stock_control_holdbacks ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_control_holdbacks FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON stock_control_holdbacks
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_insert ON stock_control_holdbacks
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_update ON stock_control_holdbacks
        FOR UPDATE USING (current_tenant_matches(tenant_id))
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE ON stock_control_holdbacks TO atlas_app;
