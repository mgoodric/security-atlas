-- security-atlas #2240 check: overlay edges whose requirement belongs to
-- another tenant's private framework version.
--
-- Migration 20261234000000 refuses such a row on INSERT and UPDATE, but does
-- not re-check rows written before it. This lists any that exist.
--
-- Read-only. It runs in a READ ONLY transaction that is rolled back, and
-- prints ids and codes, never rationale or source text. Run it as a role
-- that can see every tenant (the migrate/owner role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2240-overlay-requirement-tenant.sql
--
-- Zero rows is the expected result: the API resolves requirements in the
-- global catalog only (#1797 D5). Any row found needs a decision before it
-- is retired or deleted; do not fix it from this script.

BEGIN TRANSACTION READ ONLY;

SELECT e.id            AS overlay_edge_id,
       e.tenant_id     AS overlay_tenant_id,
       e.status,
       fv.tenant_id    AS requirement_tenant_id,
       f.slug          AS framework,
       fv.version      AS framework_version,
       fr.code         AS requirement_code
FROM fw_to_scf_overlay_edges e
JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
JOIN framework_versions fv ON fv.id = fr.framework_version_id
JOIN frameworks f ON f.id = fv.framework_id
WHERE fv.tenant_id IS NOT NULL
  AND fv.tenant_id <> e.tenant_id
ORDER BY e.tenant_id, e.created_at;

ROLLBACK;
