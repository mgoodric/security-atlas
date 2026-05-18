-- Slice 124 — defense-in-depth role probe for the unified audit-log endpoint.
--
-- Returns TRUE when the caller holds 'auditor' OR 'grc_engineer' in user_roles
-- under their tenant context, FALSE otherwise. Runs under
-- `tenancy.ApplyTenant`; RLS on user_roles enforces the tenant scope.
-- The caller checks `IsAdmin` separately and short-circuits before invoking
-- this query so the SQL only checks the two non-admin roles.

-- name: HasUnifiedAuditLogRole :one
SELECT EXISTS (
    SELECT 1
    FROM user_roles
    WHERE tenant_id = $1
      AND user_id   = $2
      AND role IN ('auditor', 'grc_engineer')
) AS allowed;
