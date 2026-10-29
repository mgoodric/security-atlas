#!/usr/bin/env bash
#
# delivery-replay.sh -- falsifier for isa/assessor-delivery.md ISC-2: "Every
# departure is reconstructible from the ledger." Falsifier: a delivered bundle
# whose evidence-record set and content hashes cannot be recovered from
# `assessor_deliveries` and shown to equal what was sent.
#
# The check, per delivery row: take the record ids from its manifest, look each
# one up in `evidence_records` (append-only, ADR-0012), and rebuild the
# manifest digest from the ledger's hashes rather than the manifest's. The
# rebuilt digest must equal `manifest_digest`, the digest recorded at send
# time. A record id that no longer resolves, or a ledger hash that differs from
# what was sent, changes the digest. The canonical digest form is DL_DIGEST in
# isa/probes/_delivery-lib.sh.
#
# Scope, from the epic's answered questions (isa/assessor-delivery.md, Not yet
# specified):
#   - Redelivery appends a row, so every row is replayed on its own. Two rows
#     for one period are two departures, not a conflict.
#   - A partial or failed delivery is replayed against what was transmitted,
#     not what the adapter says it accepted (anti-claim A1). Every row is
#     checked whatever its status.
#   - The digest covers the evidence-record set and hashes the claim names. It
#     is not a byte-for-byte rebuild of the signed OSCAL bundle; that waits for
#     the adapter, whose bundle the delivery path does not produce yet.
#
# Adopted from the engine draft (#1626), which assumed one ledger row per
# record and reported an empty ledger as cannot-run. Here a departure is one
# row with a manifest, and an empty ledger is not special: zero departures
# replay trivially, and the claim stays open on the unbuilt check until the
# tables exist.
#
# Today: no `assessor_deliveries` table, so a departure is recorded nowhere.
# The OSCAL download leaves no row at all. FALSE.
#
# Exit 0  every recorded departure replays to its recorded digest.
# Exit 1  one does not, or the tables do not exist.
# Exit 3  cannot run (see isa/probes/_delivery-lib.sh).
set -uo pipefail

name=delivery-replay
claim=ISC-2
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

CHECK="
SELECT d.id, d.status, d.manifest_digest, r.digest, r.unresolved
FROM assessor_deliveries d
CROSS JOIN LATERAL (
  SELECT $DL_DIGEST AS digest, count(*) FILTER (WHERE resolved IS NULL) AS unresolved
  FROM (
    SELECT m->>'evidence_record_id' AS rid,
           coalesce(er.hash, '<unresolved>') AS rhash,
           er.id AS resolved
    FROM jsonb_array_elements(d.manifest) m
    LEFT JOIN evidence_records er ON er.id::text = m->>'evidence_record_id') s
) r
WHERE r.digest IS DISTINCT FROM d.manifest_digest OR r.unresolved > 0
ORDER BY d.id;"

# The ledger's hash for a delivered record differs from the hash that was sent.
VIOLATION="
UPDATE evidence_records SET hash = 'hash-e2-not-what-was-sent'
WHERE id = '00000000-0000-0000-0000-0000000000e2';"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION"
dl_require_tables "$name" "$claim" \
  "A departure is recorded nowhere: the OSCAL bundle download (internal/api/oscalexport) leaves no row naming the records or hashes it carried, so nothing that left can be recovered and compared." \
  assessor_deliveries evidence_records
dl_check "$name" "$claim" "$CHECK" "delivery_id	status	recorded_digest	rebuilt_digest	unresolved_records"

echo "$name: ok -- every recorded departure replays from the ledger to its recorded digest"
exit 0
