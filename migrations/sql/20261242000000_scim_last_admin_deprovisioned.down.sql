-- Reverses 20261242000000_scim_last_admin_deprovisioned.sql.
--
-- The restored CHECK fails while any 'tenant_last_admin_deprovisioned' row
-- exists. That is deliberate: scim_audit_log is append-only, and a down
-- migration does not delete audit rows to make itself succeed.

DROP FUNCTION IF EXISTS notify_super_admins_last_admin_deprovisioned(UUID, UUID);

ALTER TABLE scim_audit_log
    DROP CONSTRAINT IF EXISTS scim_audit_log_action_check;

ALTER TABLE scim_audit_log
    ADD CONSTRAINT scim_audit_log_action_check
    CHECK (action IN (
        'user.provision',
        'user.replace',
        'user.patch',
        'user.deprovision',
        'user.reprovision',
        'user.delete'
    ));
