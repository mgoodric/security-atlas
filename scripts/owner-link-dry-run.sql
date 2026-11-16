-- #2129 dry run: what 20261218000000_risk_vendor_owner_users.sql would do to
-- the free-text risk and vendor owners, per tenant, without writing.
--
-- Read-only and counts-only: it prints no owner text, names or emails. Run
-- it as the migrate role (atlas_migrate, BYPASSRLS) BEFORE the migration,
-- inside a read-only transaction:
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/owner-link-dry-run.sql
--
-- The matching is the migration's, copied: exact name or email,
-- case-insensitive, ignoring surrounding spaces, among the record's own
-- tenant's active users. matched = exactly one user; ambiguous = two or
-- more; unmatched = none. Records with empty text are counted as empty.

BEGIN TRANSACTION READ ONLY;

WITH owners AS (
    SELECT 'risk'::text AS kind, r.id, r.tenant_id, btrim(r.treatment_owner) AS owner_text
      FROM risks r
    UNION ALL
    SELECT 'vendor', v.id, v.tenant_id, btrim(v.owner_user)
      FROM vendors v
), counted AS (
    SELECT o.kind, o.tenant_id, o.owner_text,
           (SELECT count(DISTINCT u.id)
              FROM users u
             WHERE u.tenant_id = o.tenant_id
               AND u.status = 'active'
               AND o.owner_text <> ''
               AND (lower(btrim(u.display_name)) = lower(o.owner_text)
                    OR lower(btrim(u.email)) = lower(o.owner_text))) AS n_users
      FROM owners o
)
SELECT tenant_id,
       kind,
       count(*) FILTER (WHERE owner_text <> '' AND n_users = 1) AS matched,
       count(*) FILTER (WHERE owner_text <> '' AND n_users > 1) AS ambiguous,
       count(*) FILTER (WHERE owner_text <> '' AND n_users = 0) AS unmatched,
       count(*) FILTER (WHERE owner_text = '')                  AS empty
  FROM counted
 GROUP BY tenant_id, kind
 ORDER BY tenant_id, kind;

ROLLBACK;
