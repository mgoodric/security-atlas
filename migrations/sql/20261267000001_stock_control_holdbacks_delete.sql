-- security-atlas -- #2599: the boot seed clears a stale held-back row.
--
-- stock_control_holdbacks (#2358) records a shipped manifest the boot seed
-- did not apply. When a later boot finds the tenant's active revision
-- already is the shipped manifest, nothing is held back and the upload
-- deletes the row. The app role needs DELETE, bound to the tenant like the
-- other policies.

DO $$
BEGIN
    CREATE POLICY tenant_delete ON stock_control_holdbacks
        FOR DELETE USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT DELETE ON stock_control_holdbacks TO atlas_app;
