-- Reverses 20261272000000_exception_idempotency_keys_sweep.sql (#2714).
REVOKE DELETE ON exception_idempotency_keys FROM atlas_app;
DROP POLICY IF EXISTS tenant_delete ON exception_idempotency_keys;
