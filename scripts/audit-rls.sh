#!/usr/bin/env bash
#
# audit-rls.sh — assert every public-schema table with a `tenant_id` column
# carries (a) at least one RLS policy and (b) FORCE ROW LEVEL SECURITY.
#
# Constitutional invariant 6 (CLAUDE.md): "Tenant isolation is enforced at
# the database layer via PostgreSQL Row-Level Security on every tenant-scoped
# table. Not application code. RLS denies on missing context."
#
# This script is the machine check that nothing drifts away from that
# invariant. It is run by `just audit-rls` and by CI between migrate-up and
# the integration-test suite.
#
# Required env:
#   DATABASE_URL  — connection string to a role with full pg_catalog visibility
#                   (atlas_migrate; BYPASSRLS is fine and recommended so the
#                   audit cannot be silently filtered).
#
# Exit codes:
#   0 — all tenant-scoped tables are covered
#   1 — at least one tenant-scoped table is missing a policy or FORCE
#   2 — environment misconfigured (missing DATABASE_URL, psql not on PATH,
#       connection failure)

set -Eeuo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "audit-rls: DATABASE_URL is not set" >&2
  exit 2
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "audit-rls: psql not on PATH" >&2
  exit 2
fi

# Query plan:
#   1. Find every regular table in the `public` schema that has a column
#      literally named `tenant_id`. The audit only applies to tenant-scoped
#      tables; global catalogs (e.g. scf_anchors) are excluded by this filter.
#   2. Join pg_class so we can read relforcerowsecurity per table.
#   3. Left-join pg_policy so we can count policies; tables with zero
#      policies appear with policy_count = 0.
#   4. Emit one tab-separated row per offending table to stdout.
#
# The audit deliberately does NOT validate which policy name shape is used.
# The schema today mixes single `tenant_isolation`, four-policy
# (`tenant_read`/`tenant_write`/`tenant_update`/`tenant_delete`), append-only
# (`tenant_read` + `tenant_insert/write`), and `tenant_or_catalog` shapes.
# All are intentional. Future drift surfaces in human review of the
# migration; the audit's job is the schema-level invariant: there is at
# least one policy AND FORCE is on.

readonly AUDIT_SQL=$(cat <<'SQL'
WITH tenant_tables AS (
    SELECT
        c.oid                AS reloid,
        c.relname            AS relname,
        c.relforcerowsecurity AS force_rls
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_attribute a ON a.attrelid = c.oid
    WHERE c.relkind   = 'r'
      AND n.nspname   = 'public'
      AND a.attname   = 'tenant_id'
      AND NOT a.attisdropped
),
policy_counts AS (
    SELECT polrelid AS reloid, count(*) AS n
    FROM pg_policy
    GROUP BY polrelid
)
SELECT t.relname,
       coalesce(p.n, 0) AS policies,
       t.force_rls
FROM tenant_tables t
LEFT JOIN policy_counts p ON p.reloid = t.reloid
WHERE coalesce(p.n, 0) = 0
   OR t.force_rls = false
ORDER BY t.relname
SQL
)

# -A: unaligned, -t: tuples only, -F $'\t': tab field separator.
# ON_ERROR_STOP guarantees psql exits non-zero on connection failure.
offenders="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -F $'\t' -c "$AUDIT_SQL")"

if [[ -z "$offenders" ]]; then
  echo "audit-rls: ok — all tenant-scoped tables carry a policy + FORCE"
  exit 0
fi

echo "audit-rls: FAIL — tenant-scoped tables missing policy or FORCE:" >&2
echo "table_name	policy_count	force_rls" >&2
printf '%s\n' "$offenders" >&2
exit 1
