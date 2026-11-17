-- Reverses 20261243000000_scim_last_admin_notifier_validate_user.sql: restores
-- the #2273 body of notify_super_admins_last_admin_deprovisioned, without the
-- user-argument check.

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
