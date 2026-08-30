#!/usr/bin/env bash
#
# delivery-tenant-isolation.sh — falsifier for isa/assessor-delivery.md ISC-7
# (D4 · Tenancy): "A delivery carries exactly one tenant's evidence.
# Falsifier: a delivered bundle containing a record whose tenant differs from
# the destination's."
#
# There is no assessor-delivery code yet (the epic is phase: specified,
# progress: 0 — see isa/assessor-delivery.md). The epic's own Out of Scope
# section pins what a "delivered bundle" IS today, before a line of the epic
# is written: "Replacing the OSCAL bundle. Delivery transports what already
# exists." So this probe attacks the thing delivery will transport verbatim —
# the sampled-evidence-id set `internal/oscal` already reads out of
# `sample_evidence` for the Assessment Results artifact
# (internal/db/dbx.ListSampledEvidenceForPeriod, `oscal_export.sql`), which is
# exactly the query shape reproduced below.
#
# The attack: `sample_evidence.evidence_record_id` carries only a
# SINGLE-COLUMN foreign key to evidence_records(id) — not a composite
# (tenant_id, id) key — because evidence_records has no UNIQUE(tenant_id, id)
# to hang one off. The migration says so itself
# (migrations/sql/20260511000010_audit_samples.sql, right above
# sample_evidence_evidence_fk): "Cross-tenant linkage is still blocked: RLS on
# sample_evidence requires the tenant_id match, and the store-side INSERT
# path resolves the evidence_record_id within the active tenant scope before
# writing." Read closely, that is an admission: nothing in the SCHEMA ties
# sample_evidence.evidence_record_id to sample_evidence.tenant_id. The
# invariant lives entirely in application-code discipline — the exact
# anti-pattern ADR-0011 / CLAUDE.md invariant #6 rejects for every other
# tenant boundary in this codebase ("Not application code... it depends on
# every query, written by every contributor, never forgetting").
#
# Postgres's own documented RLS caveat makes this concrete: foreign-key
# constraint checks always bypass row security on the referenced table (the
# FK just needs the row to EXIST, not to be visible to the inserting
# session). So a session scoped to tenant A, holding nothing but the ordinary
# least-privileged atlas_app role, can INSERT a sample_evidence row it owns
# (tenant_id = A, passing the tenant_write RLS check) whose
# evidence_record_id names a REAL evidence_records row that actually belongs
# to tenant B — the FK is satisfied because the row exists, full stop.
#
# The probe runs this for real against a live Postgres with RLS enforced (no
# bypass, no admin trick for the attack step itself):
#   1. Seed two tenants (A, B), each with a control + one evidence record.
#   2. Seed tenant A a population + sample (the normal, legitimate objects a
#      real draw would produce).
#   3. As the ordinary tenant-A-scoped app role, INSERT one sample_evidence
#      row for A's sample whose evidence_record_id is tenant B's evidence
#      record id.
#   4. As the SAME tenant-A-scoped app role, run the exact predicate
#      ListSampledEvidenceForPeriod uses (`tenant_id = $1 AND sample_id =
#      $2`) — the read path a delivered bundle's sampled-evidence set comes
#      from — and see whether it returns tenant B's evidence id as part of
#      "tenant A's" delivered set.
#   5. As the admin (migration) role, resolve that returned id's TRUE owning
#      tenant and confirm it differs from A.
#
# Exit 0  — the attack was rejected somewhere in this chain (claim holds
#           under this attack).
# Exit 1  — a delivered-shaped read, scoped to tenant A under ordinary RLS,
#           returned an evidence record whose true tenant is B (claim is
#           genuinely false: a delivered bundle mixing tenants, exactly as
#           ISC-7 warns).
# Exit 3  — a precondition was missing (no psql, no DATABASE_URL /
#           DATABASE_URL_APP, unreachable DB, or migrations not applied), so
#           nothing was proven about the claim either way.

set -u

fail_cannot_run() {
  echo "CANNOT RUN: $1" >&2
  exit 3
}

command -v psql >/dev/null 2>&1 || fail_cannot_run "psql not on PATH"
[[ -n "${DATABASE_URL:-}" ]] || fail_cannot_run "DATABASE_URL not set (migration/owner role — see AGENTS.md integration harness)"
[[ -n "${DATABASE_URL_APP:-}" ]] || fail_cannot_run "DATABASE_URL_APP not set (RLS-bound app role — see AGENTS.md integration harness)"

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -tAc "SELECT 1" >/dev/null 2>&1 \
  || fail_cannot_run "cannot reach Postgres via DATABASE_URL"

present="$(psql "$DATABASE_URL" -tAc "
  SELECT count(*) FROM pg_class
  WHERE relnamespace = 'public'::regnamespace
    AND relname IN ('controls','evidence_records','populations','samples','sample_evidence')
" 2>/dev/null)"
[[ "$present" == "5" ]] || fail_cannot_run "controls/evidence_records/populations/samples/sample_evidence not all present — migrations not applied"

# ----- fixture ids -----
read -r TENANT_A TENANT_B CONTROL_A CONTROL_B EVIDENCE_A EVIDENCE_B POPULATION_A SAMPLE_A < <(
  psql "$DATABASE_URL" -tA -F' ' -c \
    "SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid()"
) || fail_cannot_run "could not generate fixture ids from DATABASE_URL"
[[ -n "${SAMPLE_A:-}" ]] || fail_cannot_run "fixture id generation returned an incomplete row"

cleanup() {
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 >/dev/null 2>&1 <<SQL
DELETE FROM sample_evidence WHERE tenant_id IN ('$TENANT_A', '$TENANT_B');
DELETE FROM samples WHERE tenant_id IN ('$TENANT_A', '$TENANT_B');
DELETE FROM populations WHERE tenant_id IN ('$TENANT_A', '$TENANT_B');
DELETE FROM evidence_records WHERE tenant_id IN ('$TENANT_A', '$TENANT_B');
DELETE FROM controls WHERE tenant_id IN ('$TENANT_A', '$TENANT_B');
SQL
}
trap cleanup EXIT

# ----- seed: two tenants, each with a control + one evidence record;
#             tenant A additionally gets a population + sample (the ordinary
#             objects a real draw produces) -----
if ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 >/dev/null 2>&1 <<SQL
INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id, owner_role)
VALUES ('$CONTROL_A', '$TENANT_A', 'ISC-7 probe control A', 'probe', 'automated', 'bundle-isc7-a', 'control_owner');
INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id, owner_role)
VALUES ('$CONTROL_B', '$TENANT_B', 'ISC-7 probe control B', 'probe', 'automated', 'bundle-isc7-b', 'control_owner');

INSERT INTO evidence_records (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
VALUES ('$EVIDENCE_A', '$TENANT_A', '$CONTROL_A', '$CONTROL_A', now(), '{}'::jsonb, 'pass', 'isc7-probe-$EVIDENCE_A');
INSERT INTO evidence_records (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash)
VALUES ('$EVIDENCE_B', '$TENANT_B', '$CONTROL_B', '$CONTROL_B', now(), '{}'::jsonb, 'pass', 'isc7-probe-$EVIDENCE_B');

INSERT INTO populations (id, tenant_id, control_id, scope_predicate, time_window_start, time_window_end, created_by)
VALUES ('$POPULATION_A', '$TENANT_A', '$CONTROL_A', '{}'::jsonb, now() - interval '90 days', now(), 'isc7-probe');

INSERT INTO samples (id, tenant_id, population_id, n, seed, created_by)
VALUES ('$SAMPLE_A', '$TENANT_A', '$POPULATION_A', 1, 'isc7-probe-seed', 'isc7-probe');
SQL
then
  fail_cannot_run "fixture seed failed — schema drift from what this probe expects (see comments)"
fi

# ----- attack: as the ORDINARY tenant-A-scoped app role (no admin bypass),
#               insert one sample_evidence row A owns whose
#               evidence_record_id names TENANT B's evidence record -----
ATTACK_OUT="$(psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -tA <<SQL 2>&1
BEGIN;
SELECT set_config('app.current_tenant', '$TENANT_A', true) AS _tenant \gset
INSERT INTO sample_evidence (sample_id, tenant_id, evidence_record_id, ordinal)
VALUES ('$SAMPLE_A', '$TENANT_A', '$EVIDENCE_B', 0);
COMMIT;
SQL
)"
ATTACK_STATUS=$?

if [[ $ATTACK_STATUS -ne 0 ]]; then
  if echo "$ATTACK_OUT" | grep -qiE "row-level security policy|violates foreign key constraint|violates check constraint|permission denied"; then
    echo "the DB rejected a tenant-A-owned sample_evidence row referencing tenant B's evidence_record_id:"
    echo "$ATTACK_OUT"
    echo "TRUE: cross-tenant sample_evidence linkage is blocked (unexpectedly — the schema's own comment says only application code guards this)."
    exit 0
  fi
  echo "attack insert errored for a reason that isn't a recognizable security rejection:" >&2
  echo "$ATTACK_OUT" >&2
  fail_cannot_run "attack insert against DATABASE_URL_APP failed for an unrecognized reason — see stderr above; this is a probe/environment problem, not evidence the claim holds"
fi

# ----- read: the exact predicate ListSampledEvidenceForPeriod uses
#             (internal/db/dbx/oscal_export.sql — "se.tenant_id = $1 AND
#             se.sample_id = ..."), scoped to tenant A under ordinary RLS —
#             this is the read path a delivered bundle's sampled-evidence
#             set comes from -----
DELIVERED_OUT="$(psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -tA <<SQL 2>&1
BEGIN;
SELECT set_config('app.current_tenant', '$TENANT_A', true) AS _tenant \gset
SELECT evidence_record_id FROM sample_evidence
WHERE tenant_id = '$TENANT_A' AND sample_id = '$SAMPLE_A'
ORDER BY ordinal;
COMMIT;
SQL
)"
DELIVERED_STATUS=$?
[[ $DELIVERED_STATUS -eq 0 ]] || fail_cannot_run "delivered-shaped read against DATABASE_URL_APP failed: $DELIVERED_OUT"
DELIVERED_ID="$(echo "$DELIVERED_OUT" | tr -d '[:space:]')"

if [[ "$DELIVERED_ID" != "$EVIDENCE_B" ]]; then
  echo "attack insert succeeded, but the delivered-shaped read (tenant_id = A AND sample_id = A's sample) did not surface tenant B's evidence id — got: '$DELIVERED_ID'"
  echo "TRUE: this attack did not produce a cross-tenant record in the delivered read path."
  exit 0
fi

# ----- ground truth: whose evidence record did the delivered read just
#             hand back, really? -----
TRUE_TENANT="$(psql "$DATABASE_URL" -tAc "SELECT tenant_id FROM evidence_records WHERE id = '$EVIDENCE_B'" 2>/dev/null | tr -d '[:space:]')"

if [[ "$TRUE_TENANT" == "$TENANT_B" ]]; then
  echo "FALSE: a tenant-A-scoped read of sample_evidence — the exact predicate ListSampledEvidenceForPeriod uses to assemble the sampled-evidence set for the exported/delivered bundle — returned evidence_record_id $EVIDENCE_B, whose TRUE owning tenant is $TENANT_B, not the delivering tenant $TENANT_A."
  echo "The write that produced this was an ordinary INSERT under the least-privileged atlas_app role, RLS fully enforced, with no admin bypass: sample_evidence.evidence_record_id carries only a single-column FK to evidence_records(id) (migrations/sql/20260511000010_audit_samples.sql), so nothing at the database layer ties the referenced record's tenant to the referencing row's own tenant_id. The invariant assessor-delivery's Out-of-Scope commits to inheriting (\"Delivery transports what already exists\") is, at the point this probe attacks, enforced by application code alone — CLAUDE.md invariant #6 names that pattern as rejected for every other tenant boundary in this codebase."
  exit 1
fi

echo "CANNOT RUN: attack insert and delivered-shaped read both succeeded, but ground truth lookup for $EVIDENCE_B returned tenant '$TRUE_TENANT' (expected $TENANT_B) — inconsistent with the fixture this probe just seeded" >&2
exit 3
