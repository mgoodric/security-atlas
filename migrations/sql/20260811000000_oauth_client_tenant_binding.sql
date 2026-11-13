-- security-atlas — OAuth clients are bound to a tenant
-- (security-atlas#1739, spec #1758).
--
-- BACKGROUND:
--
--   A client_credentials token used to be tenant-free, so a machine write
--   (`atlas-cli controls upload --client-id ...`, which bootstrap.sh runs)
--   landed in the all-zeros tenant and was invisible to every user. A
--   client now carries the tenant its tokens act in, and the token
--   endpoint stamps it as the token's current tenant. A write whose
--   credential still resolves to the all-zeros tenant is refused by
--   internal/api/tenancymw.
--
-- WHAT IT DOES:
--
--   1. Adds oauth_clients.bound_tenant_id (NULL = unbound). ON DELETE SET NULL:
--      deleting a tenant leaves its clients unbound, whose writes are
--      refused, rather than blocking the delete. Named bound_tenant_id, not
--      tenant_id: the row is a platform identity, not tenant-owned data,
--      and scripts/audit-rls.sh treats every `tenant_id` column as
--      tenant-scoped (RLS + FORCE required), which this table must not be.
--   2. Binds every unbound client to the only tenant on a single-tenant
--      install (the all-zeros tenant is not a tenant). With zero tenants,
--      or two or more, nothing is bound: on a multi-tenant install the
--      operator binds each client with `atlas-cli oauth bind-client`.
--      A NOTICE says which happened.
--
--   oauth_clients stays RLS-exempt (see 20260521000010_oauth_clients.sql):
--   the token endpoint reads it before any tenant is known.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   ADD COLUMN IF NOT EXISTS, and the bind only touches unbound rows, so
--   re-applying the file binds only clients issued unbound since. The down
--   migration drops the column.

-- ===== 1. oauth_clients.bound_tenant_id =====

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS bound_tenant_id UUID NULL
        REFERENCES tenants (id) ON DELETE SET NULL;

-- ===== 2. Bind unbound clients on a single-tenant install =====

DO $$
DECLARE
    nil_tenant   CONSTANT uuid := '00000000-0000-0000-0000-000000000000';
    tenant_count int;
    the_tenant   uuid;
    bound        int;
BEGIN
    SELECT COUNT(*) INTO tenant_count FROM tenants WHERE id <> nil_tenant;
    IF tenant_count <> 1 THEN
        RAISE NOTICE 'oauth_client_tenant_binding: % tenants exist, not a single-tenant install; no client bound (use atlas-cli oauth bind-client)',
            tenant_count;
        RETURN;
    END IF;
    SELECT id INTO the_tenant FROM tenants WHERE id <> nil_tenant;

    UPDATE oauth_clients SET bound_tenant_id = the_tenant WHERE bound_tenant_id IS NULL;
    GET DIAGNOSTICS bound = ROW_COUNT;
    RAISE NOTICE 'oauth_client_tenant_binding: bound % client(s) to tenant %', bound, the_tenant;
END
$$;
