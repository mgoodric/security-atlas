#!/usr/bin/env bash
#
# no-duplicate-controls.sh -- falsifier for isa/ISA.md ISC-2: "No control in
# the catalog is duplicated across frameworks." The claim's own falsifier:
# two control rows resolving to the same SCF anchor with overlapping framework
# satisfactions. It is C1 (one control, N framework satisfactions) stated as
# something queryable.
#
# What "duplicated" means here is taken from the coverage code, not invented:
# a framework requirement is satisfied by a tenant's controls through
# controls.scf_anchor_id -> fw_to_scf_edges (internal/db/queries/dashboard.sql,
# covering_control), counting only active rows (superseded_by IS NULL) and
# only real edges (relationship_type <> 'no_relationship'). Satisfactions are
# derived from the anchor, so two active controls in one tenant on one anchor
# that has any real edge satisfy exactly the same requirements. That pair is
# the duplicate. A superseded version is not a duplicate; it is history.
#
# Two checks, and the claim is false if either finds a duplicate:
#
#   1. live scan  -- any duplicate already in this database, in any tenant.
#   2. prevention -- whether the database refuses to CREATE one. A fixture is
#      built inside BEGIN ... ROLLBACK: one SCF anchor with real edges into two
#      frameworks, and two active controls in one tenant on that anchor. If
#      the second insert is accepted, nothing stops the catalog drifting into
#      the state ISC-2 says it never reaches.
#
# Why the fixture answers the claim without the real SCF import (isa/ISA.md
# "Not yet specified", now answered there): a scan alone of an empty or
# freshly seeded catalog passes trivially and proves nothing. Whether a
# duplicate CAN exist is a property of the schema, not of which anchors
# happen to be loaded; a guard keyed on (tenant, anchor) holds for every
# anchor the import will ever load, or for none. Check 1 still covers the real
# import wherever this probe runs against one.
#
# The bar is the database, not the service layer, on purpose: controls are
# written by bundle upload, OSCAL import, control import and the demo seed,
# and a check in one writer is no check on the others.
#
# Env:
#   DATABASE_URL -- the migrate/owner role. The fixture writes the global
#                   catalog tables (frameworks, scf_anchors, fw_to_scf_edges),
#                   which the app role cannot, and the scan must see every
#                   tenant.
#
# Exit 0  no duplicate exists and the database refuses to create one.
# Exit 1  a duplicate exists, or the database accepted one (claim false).
# Exit 3  cannot run: no psql, no DATABASE_URL, no connection, no schema, or
#         the fixture failed for a reason unrelated to the claim.
set -uo pipefail

name=no-duplicate-controls

if ! command -v psql >/dev/null 2>&1; then
  echo "$name: CANNOT RUN -- psql not on PATH" >&2
  exit 3
fi
if [ -z "${DATABASE_URL:-}" ]; then
  echo "$name: CANNOT RUN -- DATABASE_URL is not set" >&2
  exit 3
fi

psql_owner() { psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -A -t "$@"; }

ready=$(psql_owner -c "SELECT to_regclass('public.controls') IS NOT NULL AND to_regclass('public.fw_to_scf_edges') IS NOT NULL" 2>&1)
if [ "$ready" != "t" ]; then
  echo "$name: CANNOT RUN -- no migrated schema at DATABASE_URL: $ready" >&2
  exit 3
fi

# ---- 1. live scan ----------------------------------------------------------
scan_sql=$(cat <<'SQL'
SELECT c.tenant_id || ' ' || a.scf_id || ' ' || count(*) || ' active controls'
FROM controls c
JOIN scf_anchors a ON a.id = c.scf_anchor_id
WHERE c.superseded_by IS NULL
  AND EXISTS (
      SELECT 1 FROM fw_to_scf_edges e
      WHERE e.scf_anchor_id = c.scf_anchor_id
        AND e.relationship_type <> 'no_relationship')
GROUP BY c.tenant_id, a.scf_id
HAVING count(*) > 1
ORDER BY 1;
SQL
)
if ! dupes=$(psql_owner -c "$scan_sql" 2>&1); then
  echo "$name: CANNOT RUN -- live scan failed: $dupes" >&2
  exit 3
fi

# ---- 2. prevention -----------------------------------------------------------
# SET CONSTRAINTS ALL IMMEDIATE so a deferred guard (a constraint trigger)
# fires at the insert rather than at a COMMIT this transaction never reaches.
# The duplicate insert runs in a DO block that catches only integrity
# violations (SQLSTATE class 23) and a trigger's RAISE (P0001): those are a
# guard refusing the duplicate. Any other error escapes, fails psql, and is
# reported as cannot-run rather than read as a verdict.
fixture_sql=$(cat <<'SQL'
BEGIN;
SET CONSTRAINTS ALL IMMEDIATE;

CREATE TEMP TABLE isc2_ids ON COMMIT DROP AS
SELECT gen_random_uuid() AS tenant,
       gen_random_uuid() AS scf_fw,  gen_random_uuid() AS scf_ver, gen_random_uuid() AS anchor,
       gen_random_uuid() AS fw_a,    gen_random_uuid() AS ver_a,   gen_random_uuid() AS req_a,
       gen_random_uuid() AS fw_b,    gen_random_uuid() AS ver_b,   gen_random_uuid() AS req_b;

INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
SELECT scf_fw, NULL::uuid, 'isa-probe ISC-2 SCF', 'isa-probe-isc2-scf-' || scf_fw, 'isa-probe' FROM isc2_ids
UNION ALL SELECT fw_a, NULL, 'isa-probe ISC-2 A', 'isa-probe-isc2-a-' || fw_a, 'isa-probe' FROM isc2_ids
UNION ALL SELECT fw_b, NULL, 'isa-probe ISC-2 B', 'isa-probe-isc2-b-' || fw_b, 'isa-probe' FROM isc2_ids;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
SELECT scf_ver, NULL::uuid, scf_fw, 'probe', 'current'::framework_version_status FROM isc2_ids
UNION ALL SELECT ver_a, NULL, fw_a, 'probe', 'current'::framework_version_status FROM isc2_ids
UNION ALL SELECT ver_b, NULL, fw_b, 'probe', 'current'::framework_version_status FROM isc2_ids;

INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
SELECT anchor, scf_ver, 'ISA-PROBE-ISC2', 'probe', 'isa-probe ISC-2 anchor' FROM isc2_ids;

INSERT INTO framework_requirements (id, framework_version_id, code, title)
SELECT req_a, ver_a, 'A-1', 'isa-probe ISC-2 requirement A-1' FROM isc2_ids
UNION ALL SELECT req_b, ver_b, 'B-1', 'isa-probe ISC-2 requirement B-1' FROM isc2_ids;

INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution)
SELECT gen_random_uuid(), req_a, anchor, 'equal'::strm_relationship_type, 1.0, 'org_internal'::crosswalk_source_attribution FROM isc2_ids
UNION ALL SELECT gen_random_uuid(), req_b, anchor, 'equal'::strm_relationship_type, 1.0, 'org_internal'::crosswalk_source_attribution FROM isc2_ids;

-- The first control on the anchor: legitimate.
INSERT INTO controls (id, tenant_id, bundle_id, scf_id, scf_anchor_id, title, control_family, implementation_type)
SELECT gen_random_uuid(), tenant, 'isa-probe-isc2-x', 'ISA-PROBE-ISC2', anchor,
       'isa-probe ISC-2 control X', 'probe', 'manual_attested'
FROM isc2_ids;

CREATE TEMP TABLE isc2_verdict (verdict text) ON COMMIT DROP;
DO $$
BEGIN
    -- The second: a different bundle, so the one-active-version-per-bundle
    -- index is not what is being tested. Same tenant, same anchor, active.
    INSERT INTO controls (id, tenant_id, bundle_id, scf_id, scf_anchor_id, title, control_family, implementation_type)
    SELECT gen_random_uuid(), tenant, 'isa-probe-isc2-y', 'ISA-PROBE-ISC2', anchor,
           'isa-probe ISC-2 control Y', 'probe', 'manual_attested'
    FROM isc2_ids;
    INSERT INTO isc2_verdict VALUES ('accepted');
EXCEPTION
    WHEN integrity_constraint_violation OR raise_exception THEN
        INSERT INTO isc2_verdict VALUES ('refused ' || SQLSTATE || ': ' || SQLERRM);
END $$;

SELECT verdict FROM isc2_verdict;
ROLLBACK;
SQL
)
if ! verdict=$(psql_owner -c "$fixture_sql" 2>&1); then
  echo "$name: CANNOT RUN -- the fixture failed for a reason unrelated to ISC-2: $verdict" >&2
  exit 3
fi
verdict=$(grep -E '^(accepted|refused)' <<<"$verdict" || true)
if [ -z "$verdict" ]; then
  echo "$name: CANNOT RUN -- the fixture produced no verdict" >&2
  exit 3
fi

failed=0
if [ -n "$dupes" ]; then
  echo "$name: FALSE -- active controls share an SCF anchor that has real framework edges (tenant, anchor, count):" >&2
  printf '  %s\n' "$dupes" >&2
  failed=1
fi
case "$verdict" in
  accepted)
    echo "$name: FALSE -- the database accepted a second active control in one tenant on an SCF anchor that already had one and that satisfies two frameworks. Nothing in the schema stops a control being duplicated (C1)." >&2
    failed=1
    ;;
  *)
    echo "$name: the database refused the duplicate control ($verdict)"
    ;;
esac

if [ "$failed" -ne 0 ]; then
  exit 1
fi
echo "$name: holds -- no duplicate in the catalog, and the schema refuses to create one"
exit 0
