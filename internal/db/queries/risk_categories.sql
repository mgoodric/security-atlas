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
