-- #1809: organisation-owned risk categories (expand step of #1745).
--
-- Every query runs under `tenancy.ApplyTenant` as `atlas_app`; the
-- four-policy RLS on org_risk_categories scopes rows to the caller's tenant.
-- The explicit tenant_id predicates keep the plans on the
-- (tenant_id, slug) unique index.

-- name: SeedRiskCategories :exec
-- Idempotently give the tenant the seven seeded categories (slugs equal
-- the old risk_category enum values, `operational` the default). A no-op
-- for a tenant that already has them.
SELECT seed_risk_categories(sqlc.arg(tenant_id)::uuid);

-- name: ListRiskCategories :many
-- The tenant's categories, active and archived, by display name.
SELECT *
FROM org_risk_categories
WHERE tenant_id = $1
ORDER BY lower(name), slug;

-- name: ResolveRiskCategory :one
-- Find one of the tenant's categories by slug or by id (its text form).
-- Slugs cannot be UUID-shaped strings that collide with another row's id
-- in practice, and the tenant predicate keeps both arms tenant-local.
SELECT *
FROM org_risk_categories
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (slug = sqlc.arg(ref)::text OR id::text = sqlc.arg(ref)::text)
LIMIT 1;

-- ===== #1810: category management =====

-- name: ListRiskCategoriesWithUsage :many
-- The tenant's categories with how many risks reference each and how many
-- aggregation rules have produced a meta-risk in it. Archived categories
-- are included only when include_archived is true.
SELECT
    c.*,
    (SELECT count(*) FROM risks r
        WHERE r.tenant_id = c.tenant_id AND r.category_id = c.id)::bigint AS risk_count,
    (SELECT count(DISTINCT ra.rule_id) FROM risk_aggregations ra
        JOIN risks p ON p.tenant_id = ra.tenant_id AND p.id = ra.parent_risk_id
        WHERE ra.tenant_id = c.tenant_id AND ra.rule_id IS NOT NULL
          AND p.category_id = c.id)::bigint AS rule_count
FROM org_risk_categories c
WHERE c.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.arg(include_archived)::boolean OR NOT c.archived)
ORDER BY lower(c.name), c.slug;

-- name: RiskCategoryUsage :one
-- The usage counts for one category, as ListRiskCategoriesWithUsage
-- computes them. Delete reads this after locking the row.
SELECT
    (SELECT count(*) FROM risks r
        WHERE r.tenant_id = sqlc.arg(tenant_id) AND r.category_id = sqlc.arg(id))::bigint AS risk_count,
    (SELECT count(DISTINCT ra.rule_id) FROM risk_aggregations ra
        JOIN risks p ON p.tenant_id = ra.tenant_id AND p.id = ra.parent_risk_id
        WHERE ra.tenant_id = sqlc.arg(tenant_id) AND ra.rule_id IS NOT NULL
          AND p.category_id = sqlc.arg(id))::bigint AS rule_count;

-- name: LockRiskCategory :one
-- Resolve a category by slug or id and lock it for the rest of the
-- transaction, so a management write sees the row it changes.
SELECT *
FROM org_risk_categories
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (slug = sqlc.arg(ref)::text OR id::text = sqlc.arg(ref)::text)
LIMIT 1
FOR UPDATE;

-- name: CreateRiskCategory :one
INSERT INTO org_risk_categories (tenant_id, slug, name, description)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateRiskCategoryText :one
-- Rename: name and description only. The slug is immutable (trigger).
UPDATE org_risk_categories
SET name = $3, description = $4
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: SetRiskCategoryArchived :one
UPDATE org_risk_categories
SET archived = $3
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: ClearRiskCategoryDefault :exec
-- First half of moving the default: the partial unique index allows one
-- default per tenant, so the old one is cleared before the new one is set.
UPDATE org_risk_categories
SET is_default = false
WHERE tenant_id = $1 AND is_default AND id <> $2;

-- name: SetRiskCategoryDefault :one
UPDATE org_risk_categories
SET is_default = true
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: DeleteRiskCategory :exec
DELETE FROM org_risk_categories
WHERE tenant_id = $1 AND id = $2;

-- ===== taxonomy_audit_log (#1810) =====
--
-- Append-only. Written in the same transaction as the change it records.

-- name: InsertTaxonomyAuditLog :exec
INSERT INTO taxonomy_audit_log (
    tenant_id, subject_kind, subject_id, subject_slug, action, before, after, actor
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
);
