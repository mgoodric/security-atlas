-- security-atlas — #2273: alert when a SCIM deprovision leaves a tenant with
-- no active admin.
--
-- SCIM deactivation (PUT/PATCH active=false, DELETE) is deliberately not
-- refused when it removes a tenant's last admin (#2261 decisions D5): the IdP
-- is saying the person has left. It must not happen silently either. The
-- SCIM store recounts the tenant's active admins in the deprovision
-- transaction; when the count goes from one or more to zero it writes a
-- scim_audit_log row and calls the function below to notify super-admins.
--
-- 1. scim_audit_log.action CHECK gains 'tenant_last_admin_deprovisioned'.
--    The six existing values are kept; the new CHECK is a superset, so every
--    existing row still conforms.
--
-- 2. notify_super_admins_last_admin_deprovisioned(tenant, user) inserts one
--    in-app notification per active super-admin, in the super-admin's own
--    tenant, through the existing notifications spine (slice 029), which the
--    email / Slack / webhook digests already deliver. It is SECURITY
--    DEFINER because the rows land in other tenants, which atlas_app's RLS
--    context cannot write or read. To keep that safe to grant to atlas_app:
--      * the caller can raise it only for its own RLS tenant;
--      * it does nothing unless the tenant really has no active admin (the
--        same predicate as CountActiveTenantAdmins);
--      * it skips a super-admin who already has an unread alert for the
--        same tenant, so repeated calls cannot flood an inbox.
--    The payload carries ids only (tenant, deprovisioned user).

-- ===== 1. scim_audit_log.action CHECK =====

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
        'user.delete',
        'tenant_last_admin_deprovisioned'
    ));

-- ===== 2. super-admin notification =====

CREATE OR REPLACE FUNCTION notify_super_admins_last_admin_deprovisioned(
    p_tenant UUID,
    p_deprovisioned_user UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_notified INTEGER;
BEGIN
    IF p_tenant IS NULL OR NOT current_tenant_matches(p_tenant) THEN
        RAISE EXCEPTION 'notify_super_admins_last_admin_deprovisioned: tenant is not the current tenant'
            USING ERRCODE = '42501';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM user_roles ur
        JOIN users u
          ON u.tenant_id = ur.tenant_id
         AND u.id::text  = regexp_replace(ur.user_id, '^user:', '')
        WHERE ur.tenant_id = p_tenant
          AND ur.role      = 'admin'
          AND u.status     = 'active'
          AND u.active
    ) THEN
        RETURN 0;
    END IF;

    INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload)
    SELECT gen_random_uuid(),
           u.tenant_id,
           u.id::text,
           'tenant.last_admin_deprovisioned',
           jsonb_build_object(
               'tenant_id', p_tenant,
               'deprovisioned_user_id', p_deprovisioned_user)
    FROM super_admins sa
    JOIN users u ON u.id = sa.user_id
    WHERE u.status = 'active'
      AND u.active
      AND NOT EXISTS (
          SELECT 1
          FROM notifications n
          WHERE n.tenant_id         = u.tenant_id
            AND n.recipient_user_id = u.id::text
            AND n.type              = 'tenant.last_admin_deprovisioned'
            AND n.read_at IS NULL
            AND n.payload->>'tenant_id' = p_tenant::text);

    GET DIAGNOSTICS v_notified = ROW_COUNT;
    RETURN v_notified;
END;
$$;

COMMENT ON FUNCTION notify_super_admins_last_admin_deprovisioned(UUID, UUID) IS
    '#2273: notify every active super-admin, in their own tenant, that a SCIM deprovision left p_tenant with no active admin. Callable only for the caller''s RLS tenant and only while that tenant has no active admin; skips a super-admin with an unread alert for the same tenant. Returns the number of notifications written.';

REVOKE ALL ON FUNCTION notify_super_admins_last_admin_deprovisioned(UUID, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION notify_super_admins_last_admin_deprovisioned(UUID, UUID) TO atlas_app;
