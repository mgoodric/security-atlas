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

-- name: ListVisibleThemesWithUsage :many
-- The visible vocabulary (defaults + the tenant's own) with each theme's
-- usage (#1818): how many of the tenant's risks carry it, and how many of
-- its aggregation rules target it. Same counts as ThemeUsage.
SELECT
    t.theme_name,
    t.description,
    t.tenant_id,
    (SELECT count(*) FROM risks r
        WHERE r.tenant_id = sqlc.arg(tenant_id)
          AND r.themes @> ARRAY[t.theme_name])::bigint AS risk_count,
    (SELECT count(*) FROM aggregation_rules ar
        WHERE ar.tenant_id = sqlc.arg(tenant_id)
          AND ar.target_theme = t.theme_name)::bigint AS rule_count
FROM org_themes t
WHERE t.tenant_id IS NULL OR t.tenant_id = sqlc.arg(tenant_id)
ORDER BY (t.tenant_id IS NULL) DESC, t.theme_name;

-- name: DeleteTenantTheme :exec
-- Tenant-private themes only — the policy forbids deleting defaults
-- regardless of what this query asks for.
DELETE FROM org_themes
WHERE tenant_id = $1 AND id = $2;

-- ===== #1811: organisation theme management =====

-- name: LockThemeVocabulary :exec
-- #1811: serialize a theme rename or delete with every theme assignment in
-- one tenant. Rename and delete take it exclusively before anything else;
-- an assignment takes the shared form (LockThemeVocabularyShared) before it
-- validates, so it can never write a name a concurrent rename or delete has
-- just removed. Transaction-scoped.
SELECT pg_advisory_xact_lock(hashtextextended('theme_vocabulary:' || sqlc.arg(tenant_id)::uuid::text, 0));

-- name: LockThemeVocabularyShared :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended('theme_vocabulary:' || sqlc.arg(tenant_id)::uuid::text, 0));

-- name: GetVisibleThemeByRef :one
-- A theme the caller can see (built-in or its own), named by name or id.
SELECT *
FROM org_themes
WHERE (tenant_id IS NULL OR tenant_id = sqlc.arg(tenant_id))
  AND (theme_name = sqlc.arg(ref)::text OR id::text = sqlc.arg(ref)::text)
ORDER BY (tenant_id IS NULL) DESC
LIMIT 1;

-- name: LockTenantTheme :one
SELECT *
FROM org_themes
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: ThemeNameTaken :one
-- Names are unique across the built-ins and the tenant's own themes; the
-- two partial unique indexes only cover each namespace on its own.
SELECT EXISTS (
    SELECT 1
    FROM org_themes
    WHERE (tenant_id IS NULL OR tenant_id = sqlc.arg(tenant_id))
      AND theme_name = sqlc.arg(theme_name)::text
      AND id <> sqlc.arg(except_id)
) AS taken;

-- name: UpdateTenantTheme :one
UPDATE org_themes
SET theme_name = sqlc.arg(theme_name),
    description = sqlc.arg(description),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: RenameThemeOnRisks :execrows
-- Replace a theme name in every one of the tenant's risk theme arrays,
-- keeping the array deduplicated and sorted as AssignThemes stores it.
UPDATE risks
SET themes = ARRAY(
        SELECT DISTINCT t
        FROM unnest(array_replace(themes, sqlc.arg(old_name)::text, sqlc.arg(new_name)::text)) AS t
        ORDER BY t
    )
WHERE tenant_id = sqlc.arg(tenant_id)
  AND themes @> ARRAY[sqlc.arg(old_name)::text];

-- name: RenameThemeOnRules :execrows
-- Retarget the tenant's aggregation rules. The engine reads target_theme
-- from rule_body, and the column is its queryable projection, so both move.
UPDATE aggregation_rules
SET target_theme = sqlc.arg(new_name)::text,
    rule_body = jsonb_set(rule_body, '{target_theme}', to_jsonb(sqlc.arg(new_name)::text)),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND target_theme = sqlc.arg(old_name)::text;

-- name: ThemeUsage :one
-- How many of the tenant's risks carry the theme, and how many of its
-- aggregation rules (any status) target it.
SELECT
    (SELECT count(*) FROM risks r
        WHERE r.tenant_id = sqlc.arg(tenant_id)
          AND r.themes @> ARRAY[sqlc.arg(theme_name)::text])::bigint AS risk_count,
    (SELECT count(*) FROM aggregation_rules ar
        WHERE ar.tenant_id = sqlc.arg(tenant_id)
          AND ar.target_theme = sqlc.arg(theme_name)::text)::bigint AS rule_count;
