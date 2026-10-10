#!/usr/bin/env bash
#
# no-duplicate-controls.sh — falsifier for ISC-2 (isa/ISA.md):
# "No control in the catalog is duplicated across frameworks."
#
# Constitutional invariant C1: one control, N framework satisfactions. The
# UCF is a graph with STRM-typed edges through SCF anchors; a control is
# never duplicated per framework.
#
# This probe does NOT scan the live catalog for duplicates — an empty or
# not-yet-imported catalog would pass that scan trivially and prove nothing
# (ISA.md "Not yet specified" flagged exactly this ambiguity). Instead it
# asks the sharper question: does the schema/service layer PREVENT a
# duplicate from being created at all? It builds a minimal fixture inside a
# transaction — one SCF anchor satisfying two independent frameworks, then
# two control rows both resolving to that anchor — and checks whether the
# second control is rejected. Everything happens inside BEGIN/ROLLBACK, so
# no fixture data survives the run.
#
# Required env:
#   DATABASE_URL  — connection string for the migrate/owner role
#                   (atlas_migrate; BYPASSRLS + write access to the global
#                   catalog tables, needed here because the fixture writes
#                   frameworks/scf_anchors/fw_to_scf_edges directly and
#                   writes a control for a synthetic tenant with no
#                   app.current_tenant session set).
#
# Exit codes:
#   0 — PASS: the DB rejected the duplicate control (invariant enforced)
#   1 — FALSE: the duplicate was created without complaint (invariant not
#       enforced — this is the claim failing)
#   3 — CANNOT RUN: a precondition is missing (no DATABASE_URL, no psql,
#       no connection, or the probe errored for a reason unrelated to the
#       invariant it is checking)

set -Eeuo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "no-duplicate-controls: DATABASE_URL is not set" >&2
  exit 3
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "no-duplicate-controls: psql not on PATH" >&2
  exit 3
fi

if ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -X -q -t -A -c 'SELECT 1' >/dev/null 2>/dev/null; then
  echo "no-duplicate-controls: cannot connect to \$DATABASE_URL" >&2
  exit 3
fi

# The fixture: one SCF anchor ("ISC2-PROBE-01") wired to two separate,
# synthetic frameworks (PROBE-A, PROBE-B) via fw_to_scf_edges — giving it
# genuine overlapping framework satisfactions, not a coincidental string
# match. Two controls are then inserted against that same scf_id. If both
# inserts succeed, the catalog now holds a control duplicated across
# frameworks — exactly what ISC-2 claims cannot happen.
readonly PROBE_SQL=$(cat <<'SQL'
BEGIN;

WITH
tenant AS MATERIALIZED (
    SELECT gen_random_uuid() AS id
),
scf_framework AS (
    INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
    VALUES (gen_random_uuid(), NULL, 'ISC-2 probe . SCF catalog', 'isc2-probe-scf', 'Secure Controls Framework')
    RETURNING id
),
scf_version AS (
    INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
    SELECT gen_random_uuid(), NULL, scf_framework.id, '2026.1-probe', 'current'
    FROM scf_framework
    RETURNING id
),
anchor AS (
    INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
    SELECT gen_random_uuid(), scf_version.id, 'ISC2-PROBE-01', 'probe', 'ISC-2 probe anchor'
    FROM scf_version
    RETURNING id, scf_id
),
fw_a AS (
    INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
    VALUES (gen_random_uuid(), NULL, 'ISC-2 probe . Framework A', 'isc2-probe-fw-a', 'probe')
    RETURNING id
),
fw_a_version AS (
    INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
    SELECT gen_random_uuid(), NULL, fw_a.id, '1.0-probe', 'current'
    FROM fw_a
    RETURNING id
),
fw_a_req AS (
    INSERT INTO framework_requirements (id, framework_version_id, code, title)
    SELECT gen_random_uuid(), fw_a_version.id, 'PROBE-A-1', 'ISC-2 probe requirement A-1'
    FROM fw_a_version
    RETURNING id, code
),
fw_b AS (
    INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
    VALUES (gen_random_uuid(), NULL, 'ISC-2 probe . Framework B', 'isc2-probe-fw-b', 'probe')
    RETURNING id
),
fw_b_version AS (
    INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
    SELECT gen_random_uuid(), NULL, fw_b.id, '1.0-probe', 'current'
    FROM fw_b
    RETURNING id
),
fw_b_req AS (
    INSERT INTO framework_requirements (id, framework_version_id, code, title)
    SELECT gen_random_uuid(), fw_b_version.id, 'PROBE-B-1', 'ISC-2 probe requirement B-1'
    FROM fw_b_version
    RETURNING id, code
),
edge_a AS (
    INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution)
    SELECT gen_random_uuid(), fw_a_req.id, anchor.id, 'equal', 1.0, 'org_internal'
    FROM fw_a_req, anchor
    RETURNING id
),
edge_b AS (
    INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution)
    SELECT gen_random_uuid(), fw_b_req.id, anchor.id, 'equal', 1.0, 'org_internal'
    FROM fw_b_req, anchor
    RETURNING id
),
control_x AS (
    INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type)
    SELECT gen_random_uuid(), tenant.id, anchor.scf_id, 'ISC-2 probe . duplicate control X', 'probe', 'manual_attested'
    FROM tenant, anchor
    RETURNING id, scf_id
),
control_y AS (
    -- The second control resolving to the SAME scf_anchor as control_x.
    -- Nothing in the schema references control_x here; if this insert
    -- succeeds unconditionally, no guard exists.
    INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type)
    SELECT gen_random_uuid(), tenant.id, anchor.scf_id, 'ISC-2 probe . duplicate control Y', 'probe', 'manual_attested'
    FROM tenant, anchor
    RETURNING id, scf_id
)
SELECT
    cx.scf_id                                                             AS scf_anchor,
    2                                                                      AS duplicate_control_count,
    (SELECT array_agg(code ORDER BY code)
       FROM (SELECT code FROM fw_a_req UNION ALL SELECT code FROM fw_b_req) reqs) AS overlapping_framework_requirements
FROM control_x cx, control_y cy;

ROLLBACK;
SQL
)

stderr_file="$(mktemp)"
trap 'rm -f "$stderr_file"' EXIT

if ! result="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -X -q -A -t -F $'\t' -c "$PROBE_SQL" 2>"$stderr_file")"; then
  stderr_output="$(cat "$stderr_file")"
  if grep -qE 'duplicate key value violates unique constraint|violates check constraint|violates exclusion constraint' <<<"$stderr_output"; then
    echo "no-duplicate-controls: ok — the database rejected the second control resolving to the same SCF anchor" >&2
    echo "$stderr_output" >&2
    exit 0
  fi
  echo "no-duplicate-controls: probe could not complete for a reason unrelated to ISC-2" >&2
  echo "$stderr_output" >&2
  exit 3
fi

if [[ -z "$result" ]]; then
  echo "no-duplicate-controls: probe ran but returned no row — the fixture did not build as expected" >&2
  exit 3
fi

echo "no-duplicate-controls: FAIL — a single SCF anchor carries two control rows with overlapping framework satisfactions, and the database accepted both without complaint:" >&2
echo "scf_anchor	duplicate_control_count	overlapping_framework_requirements" >&2
printf '%s\n' "$result" >&2
echo "no-duplicate-controls: ISC-2 claims this cannot happen (C1: one control, N framework satisfactions). Today nothing — not a unique index, not a trigger, not an application-layer check — stops it." >&2
exit 1
