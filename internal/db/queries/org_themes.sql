-- name: CreateOrgTheme :one
-- Create a tenant-private theme. tenant_id is required and must match the
-- GUC. Default themes (tenant_id IS NULL) are migration-only and not
-- creatable through this query path — the policy on org_themes forbids it.
INSERT INTO org_themes (id, tenant_id, theme_name, description)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetOrgThemeByID :one
SELECT *
FROM org_themes
WHERE id = $1
  AND (tenant_id IS NULL OR current_tenant_matches(tenant_id));

-- name: ListDefaultThemes :many
-- The 10 built-in themes (canvas §6.5). Visible to every tenant via the
-- `tenant_or_catalog_read` policy.
SELECT *
FROM org_themes
WHERE tenant_id IS NULL
ORDER BY theme_name;

-- name: ListTenantThemes :many
-- Tenant-private themes only. Caller composes with ListDefaultThemes when a
-- full visible vocabulary is needed.
SELECT *
FROM org_themes
WHERE tenant_id = $1
ORDER BY theme_name;

-- name: ListAllVisibleThemes :many
-- Defaults + tenant-private themes in one query. Order: defaults first, then
-- tenant themes alphabetically. Used by slice 053's "available themes for
-- this tenant" picker.
SELECT *
FROM org_themes
WHERE tenant_id IS NULL OR tenant_id = $1
ORDER BY (tenant_id IS NULL) DESC, theme_name;

-- name: DeleteTenantTheme :exec
-- Tenant-private themes only — the policy forbids deleting defaults
-- regardless of what this query asks for.
DELETE FROM org_themes
WHERE tenant_id = $1 AND id = $2;
