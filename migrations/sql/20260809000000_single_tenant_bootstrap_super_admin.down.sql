-- Reverses 20260809000000_single_tenant_bootstrap_super_admin.sql:
--   1. removes the super_admin grants that migration wrote;
--   2. restores super_admins.granted_via CHECK to its slice-142 shape.

DELETE FROM super_admins WHERE granted_via = 'single_tenant_upgrade';

ALTER TABLE super_admins
    DROP CONSTRAINT IF EXISTS super_admins_granted_via_chk;

ALTER TABLE super_admins
    ADD CONSTRAINT super_admins_granted_via_chk
    CHECK (granted_via IN ('bootstrap_first_install', 'manual_grant'));
