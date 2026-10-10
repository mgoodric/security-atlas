#!/usr/bin/env bash
#
# ledger-replay.sh — falsifier for ISC-3 (isa/ISA.md): "Any past date is
# reconstructible from the evidence ledger." The claim's own falsifier, named
# in the ISA, is: a replay at time T that disagrees with what the platform
# reported at T.
#
# What this probe actually checks: the production "as-of" query
# (`ListEvidenceForControlAsOf` in internal/db/queries/control_evaluations.sql,
# consumed by internal/eval/engine.go's EvaluateControl(asOf) and by ISC-3's
# own replay path) filters the ledger on `observed_at <= $as_of` ONLY. It
# never bounds by `ingested_at` (evidence_records.ingested_at, the ledger's
# own append-order timestamp). So a record that is ingested AFTER T, but
# carries a caller-supplied observed_at at or before T (a backdated manual
# upload, a connector run that pushes late, a clock-skewed source), is
# invisible to any query the platform could actually have run live at T —
# yet a replay run today with as_of=T includes it. Replaying T does not
# reproduce what the platform reported at T; it reproduces a revisionist
# version of T that only exists because of what arrived after T.
#
# This probe reproduces that gap directly against Postgres:
#   1. Insert E1: observed_at = T-10d, ingested_at = T-10d (ordinary evidence,
#      present in the ledger by the time T = "now - 5d" arrives).
#   2. Insert E2: observed_at = T-9d, ingested_at = now() (a record that
#      claims to describe something from before T, but was not written to
#      the ledger until after T — the late-arrival case).
#   3. Compute GROUND TRUTH for "what was reported at T": evidence with
#      observed_at <= T AND ingested_at <= T. E2 is excluded — it did not
#      exist in the ledger at T.
#   4. Compute REPLAY using the exact predicate the production query runs:
#      evidence with observed_at <= T (ingested_at unbounded). E2 is
#      included.
#   5. If the two record sets differ, the replay disagrees with what was
#      reported at T — ISC-3 is false today. All writes happen inside a
#      transaction that is always rolled back; the probe leaves no trace.
#
# Required env:
#   DATABASE_URL_APP — connection string for the non-owner app role
#   (atlas_app) that RLS actually binds on. Same variable `just
#   test-integration` requires (see AGENTS.md "The integration harness").
#   The owner/migrate role (DATABASE_URL) is not used here on purpose: it is
#   commonly BYPASSRLS, which would let the probe pass for the wrong reason.
#
# Exit codes:
#   0 — replay at T agrees with what was reported at T (claim holds)
#   1 — replay at T disagrees with what was reported at T (claim is false)
#   3 — precondition missing: no psql, no DATABASE_URL_APP, no DB
#       connection, or the schema (evidence_records/controls) isn't
#       migrated. Not a verdict on the claim.

set -Eeuo pipefail

fail_cannot_run() {
  echo "ledger-replay: CANNOT RUN — $1" >&2
  exit 3
}

if ! command -v psql >/dev/null 2>&1; then
  fail_cannot_run "psql not on PATH"
fi

if [[ -z "${DATABASE_URL_APP:-}" ]]; then
  fail_cannot_run "DATABASE_URL_APP is not set (see 'just db-up' / 'just migrate-up' in AGENTS.md)"
fi

if ! command -v uuidgen >/dev/null 2>&1; then
  fail_cannot_run "uuidgen not on PATH"
fi

psql_app() {
  psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -X -q "$@"
}

connerr=$(mktemp)
trap 'rm -f "$connerr"' EXIT
if ! psql_app -tAc "SELECT 1" >/dev/null 2>"$connerr"; then
  fail_cannot_run "cannot connect via DATABASE_URL_APP: $(cat "$connerr")"
fi

schema_ready=$(psql_app -tAc \
  "SELECT (to_regclass('public.evidence_records') IS NOT NULL AND to_regclass('public.controls') IS NOT NULL)")
if [[ "$schema_ready" != "t" ]]; then
  fail_cannot_run "evidence_records/controls not present — run 'just migrate-up' first"
fi

tenant=$(uuidgen | tr '[:upper:]' '[:lower:]')
control=$(uuidgen | tr '[:upper:]' '[:lower:]')
e1=$(uuidgen | tr '[:upper:]' '[:lower:]')
e2=$(uuidgen | tr '[:upper:]' '[:lower:]')

sql_file=$(mktemp)
trap 'rm -f "$connerr" "$sql_file"' EXIT

cat >"$sql_file" <<SQL
BEGIN;
SET LOCAL app.current_tenant = '${tenant}';

INSERT INTO controls (id, tenant_id, title, control_family, implementation_type)
VALUES ('${control}', '${tenant}', 'ledger-replay probe control', 'test', 'manual_attested');

-- E1: ordinary evidence — observed and ingested well before T.
INSERT INTO evidence_records
  (id, tenant_id, control_id, observed_at, ingested_at, provenance, result,
   hash, freshness_class, control_ref, ingestion_path)
VALUES
  ('${e1}', '${tenant}', '${control}',
   now() - interval '10 days', now() - interval '10 days',
   '{}'::jsonb, 'pass', 'ledger-replay-probe-h1', 'monthly',
   'probe:ledger-replay', 'manual_upload');

-- E2: late-arriving evidence — describes a moment before T (observed_at)
-- but was not written to the ledger until after T (ingested_at = now()).
INSERT INTO evidence_records
  (id, tenant_id, control_id, observed_at, ingested_at, provenance, result,
   hash, freshness_class, control_ref, ingestion_path)
VALUES
  ('${e2}', '${tenant}', '${control}',
   now() - interval '9 days', now(),
   '{}'::jsonb, 'fail', 'ledger-replay-probe-h2', 'monthly',
   'probe:ledger-replay', 'manual_upload');

-- Ground truth: what the ledger actually held at T = now() - 5 days, i.e.
-- what a live query run AT T could have returned.
SELECT coalesce(array_agg(id ORDER BY id), ARRAY[]::uuid[]) AS ids
FROM evidence_records
WHERE tenant_id = '${tenant}'
  AND control_id = '${control}'
  AND observed_at <= (now() - interval '5 days')
  AND ingested_at  <= (now() - interval '5 days')
\gset ground_

-- Replay: the exact predicate production runs today
-- (ListEvidenceForControlAsOf, internal/db/queries/control_evaluations.sql)
-- — observed_at bounded, ingested_at NOT bounded.
SELECT coalesce(array_agg(id ORDER BY id), ARRAY[]::uuid[]) AS ids
FROM evidence_records
WHERE tenant_id = '${tenant}'
  AND control_id = '${control}'
  AND observed_at <= (now() - interval '5 days')
\gset replay_

ROLLBACK;

\echo GROUND: :ground_ids
\echo REPLAY: :replay_ids
SQL

output=$(psql_app -f "$sql_file")

ground=$(echo "$output" | grep '^GROUND: ' | sed 's/^GROUND: //')
replay=$(echo "$output" | grep '^REPLAY: ' | sed 's/^REPLAY: //')

if [[ -z "$ground" || -z "$replay" ]]; then
  fail_cannot_run "probe query did not produce comparable output: $output"
fi

if [[ "$ground" == "$replay" ]]; then
  echo "ledger-replay: PASS — replay at T matches what was reported at T (${ground})"
  exit 0
fi

echo "ledger-replay: FAIL — replay at T disagrees with what was reported at T" >&2
echo "  what was actually reported at T (observed_at<=T AND ingested_at<=T): ${ground}" >&2
echo "  what replaying as_of=T returns today (observed_at<=T only):          ${replay}" >&2
echo "  cause: ListEvidenceForControlAsOf (internal/db/queries/control_evaluations.sql)" >&2
echo "  filters only on observed_at, never on ingested_at, so evidence ingested" >&2
echo "  after T with a backdated observed_at retroactively changes T's replay." >&2
exit 1
