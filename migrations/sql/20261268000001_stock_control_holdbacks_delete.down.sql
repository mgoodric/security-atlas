-- Reverses 20261268000001_stock_control_holdbacks_delete.sql (#2599).
REVOKE DELETE ON stock_control_holdbacks FROM atlas_app;
DROP POLICY IF EXISTS tenant_delete ON stock_control_holdbacks;
