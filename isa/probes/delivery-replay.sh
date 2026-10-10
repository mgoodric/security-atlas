#!/usr/bin/env bash
#
# delivery-replay.sh — falsifier for isa/assessor-delivery.md ISC-2.
#
# Claim: every departure to an assessor is reconstructible from the ledger —
# a delivered bundle's evidence-record set and content hashes can be
# recovered from `assessor_deliveries` alone and shown to equal what was
# actually sent. ISC-2 is the load-bearing claim in D1: if the replay does
# not reproduce what left, ISC-1 (registered-destination-only) is
# unfalsifiable too, since there is nothing to check the departure against.
#
# What this probe does, in order:
#   1. Confirm there is a Postgres app-role connection to probe against.
#   2. Confirm the `assessor_deliveries` ledger exists at all. It does not
#      today — the epic is unbuilt (no `assessor` symbol anywhere in
#      internal/ or migrations/sql/) — so a departure has nowhere to be
#      recorded and nothing can be recovered from it. That is ISC-2 failing,
#      not a broken probe.
#   3. Once the ledger exists: require at least one delivered row to replay.
#   4. Once a delivered row exists: rebuild the evidence-record set and
#      content-hash set from `evidence_records` (the source-of-truth
#      ledger — ADR-0012) for the record ids the delivery names, and diff
#      that reconstruction against the hashes `assessor_deliveries` recorded
#      as sent. Any mismatch, or any referenced evidence_record that no
#      longer resolves, is the claim failing for a live delivery.
#
# Schema assumption for step 4 (not yet built — D1 is unimplemented): one
# `assessor_deliveries` row per delivered evidence record, columns
# `evidence_record_id` (FK -> evidence_records.id) and `content_hash` (the
# hash recorded at send time). This mirrors the existing per-record
# `evidence_audit_log` shape (slice 013) rather than a whole-bundle-digest
# design; if D1 lands a different shape, this step's query is the first
# thing to reconcile against the real migration, not the falsifier's intent.
#
# Exit codes:
#   0 — every assessor_deliveries row's recorded evidence-record set and
#       content hashes are reproduced exactly by replaying evidence_records
#   1 — the claim does not hold (see stderr for which reason)
#   3 — CANNOT RUN: a precondition is missing (no client, no DB connection,
#       no assessor_deliveries ledger yet, or no delivered row to replay).
#       Not a verdict on the claim.
#
# Local repro (same env as `just test-integration`):
#   DATABASE_URL_APP=postgres://... bash isa/probes/delivery-replay.sh

set -Eeuo pipefail

if ! command -v psql >/dev/null 2>&1; then
  echo "delivery-replay: CANNOT RUN -- psql is not on PATH, no client to probe with" >&2
  exit 3
fi

if [[ -z "${DATABASE_URL_APP:-}" ]]; then
  echo "delivery-replay: CANNOT RUN -- DATABASE_URL_APP is unset, no app-role Postgres connection to replay against" >&2
  exit 3
fi

if ! psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -tAc 'select 1' >/dev/null 2>&1; then
  echo "delivery-replay: CANNOT RUN -- cannot connect to Postgres at DATABASE_URL_APP" >&2
  exit 3
fi

LEDGER_EXISTS="$(psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -tAc \
  "select to_regclass('public.assessor_deliveries') is not null")"

if [[ "$LEDGER_EXISTS" != "t" ]]; then
  echo "delivery-replay: FALSE -- no assessor_deliveries table exists in the schema." >&2
  echo "A departure to an assessor has nowhere to be recorded, so no delivered" >&2
  echo "bundle's evidence-record set or content hashes can ever be recovered from" >&2
  echo "the ledger and shown to equal what was sent. ISC-2 (isa/assessor-delivery.md)" >&2
  echo "does not hold." >&2
  exit 1
fi

DELIVERY_COUNT="$(psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -tAc \
  "select count(*) from assessor_deliveries")"

if [[ "$DELIVERY_COUNT" -eq 0 ]]; then
  echo "delivery-replay: CANNOT RUN -- assessor_deliveries exists but is empty, no delivered bundle to replay" >&2
  exit 3
fi

MISMATCHES="$(psql "$DATABASE_URL_APP" -v ON_ERROR_STOP=1 -tAc "
  select count(*)
  from assessor_deliveries ad
  left join evidence_records er on er.id = ad.evidence_record_id
  where er.id is null
     or er.hash is distinct from ad.content_hash
")"

if [[ "$MISMATCHES" -ne 0 ]]; then
  echo "delivery-replay: FALSE -- $MISMATCHES assessor_deliveries row(s) do not replay:" >&2
  echo "either the referenced evidence_record no longer resolves, or its content" >&2
  echo "hash disagrees with the hash assessor_deliveries recorded as sent. The" >&2
  echo "ledger cannot reconstruct what actually left for at least one departure." >&2
  exit 1
fi

echo "delivery-replay: OK -- every assessor_deliveries row replays to an equal evidence-record hash"
exit 0
