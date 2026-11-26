-- security-atlas #2524 dry run: the free-text policy audiences that exist
-- today, ahead of mapping them onto the audience enum (the five roles plus
-- 'everyone').
--
-- Read-only. Every statement runs in a READ ONLY transaction that is
-- rolled back. It prints a distinct acknowledgment_required_roles value and
-- counts, never a tenant id, a policy id, a title or any policy text. Run it
-- as a role that can see every tenant (the migrate/owner role, which
-- bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2524-policy-audiences.sql
--
-- The mapping, applied by migration
-- 20261273000000_policy_audience_generic_to_everyone.sql (#2588), which
-- ships only on Matt's go for this script's counts:
--   'employee', 'all', 'all_staff', 'contractor'  -> 'everyone'
--   any other value                               -> left as it is, and
--                                                    flagged "unrecognised
--                                                    audience" in the UI
-- Nothing here changes a row.

BEGIN TRANSACTION READ ONLY;

-- 1. Each audience value outside the enum: how many policies carry it, how
--    many of those are published (and so in someone's pending list), and
--    in how many tenants it appears.
SELECT r AS audience_value,
       count(*) AS policies,
       count(*) FILTER (WHERE p.status = 'published') AS published,
       count(DISTINCT p.tenant_id) AS tenants
FROM policies p
CROSS JOIN LATERAL unnest(p.acknowledgment_required_roles) AS r
WHERE r NOT IN ('admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer', 'everyone')
GROUP BY r
ORDER BY policies DESC, r;

-- 2. Policies by audience shape, so the counts add up to every policy:
--    'enum' names only roles and/or everyone, 'free_text' names only values
--    outside it, 'mixed' both, 'untargeted' none.
SELECT CASE
           WHEN cardinality(acknowledgment_required_roles) = 0 THEN 'untargeted'
           WHEN acknowledgment_required_roles <@ ARRAY['admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer', 'everyone']::text[] THEN 'enum'
           WHEN NOT (acknowledgment_required_roles && ARRAY['admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer', 'everyone']::text[]) THEN 'free_text'
           ELSE 'mixed'
       END AS audience_shape,
       count(*) AS policies,
       count(*) FILTER (WHERE status = 'published') AS published
FROM policies
GROUP BY 1
ORDER BY 1;

ROLLBACK;
