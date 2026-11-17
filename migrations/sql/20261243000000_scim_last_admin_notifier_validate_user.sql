-- security-atlas — #2504: the last-admin notifier validates its user argument.
--
-- notify_super_admins_last_admin_deprovisioned (#2273) is SECURITY DEFINER and
-- granted to atlas_app, and it copied p_deprovisioned_user into the alert
-- payload unchecked. It now refuses the call unless p_deprovisioned_user is an
-- existing user in p_tenant who is no longer active (the inverse of the
-- active-admin predicate: status = 'active' AND active). search_path stays
-- pinned. The rest of the body is unchanged from 20261242000000.

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

    IF p_deprovisioned_user IS NULL OR NOT EXISTS (
        SELECT 1
        FROM users u
        WHERE u.id        = p_deprovisioned_user
          AND u.tenant_id = p_tenant
          AND NOT (u.status = 'active' AND u.active)
    ) THEN
        RAISE EXCEPTION 'notify_super_admins_last_admin_deprovisioned: user is not an inactive user of the tenant'
            USING ERRCODE = '22023';
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
    '#2273/#2504: notify every active super-admin, in their own tenant, that a SCIM deprovision left p_tenant with no active admin. Callable only for the caller''s RLS tenant, only for an existing inactive user of that tenant, and only while it has no active admin; skips a super-admin with an unread alert for the same tenant. Returns the number of notifications written.';

REVOKE ALL ON FUNCTION notify_super_admins_last_admin_deprovisioned(UUID, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION notify_super_admins_last_admin_deprovisioned(UUID, UUID) TO atlas_app;
