#!/usr/bin/env bash
#
# delivery-freeze-horizon.sh -- falsifier for isa/assessor-delivery.md ISC-4:
# "A frozen period delivers nothing observed after its freeze." Falsifier: a
# delivered record whose `observed_at` is later than the period's `frozen_at`.
#
# The check: for every delivery of a frozen period, every record in its
# manifest was observed at or before the period's `frozen_at` (C10). Records
# are resolved through `evidence_records`; the period through
# `audit_periods`. A delivery of a period that is not frozen is outside the
# claim.
#
# Adopted from the engine draft (#1628), which assumed one ledger row per
# record. Its header also argued that the OSCAL export reads controls and
# policies live; those are not evidence records with an `observed_at`, so
# that argument is left out here rather than made part of this claim.
#
# Today: no `assessor_deliveries` table, so there is no delivery-time gate on
# the horizon at all. FALSE.
#
# Exit 0  no delivered record postdates its frozen period's freeze.
# Exit 1  one does, or the tables do not exist.
# Exit 3  cannot run (see isa/probes/_delivery-lib.sh).
set -uo pipefail

name=delivery-freeze-horizon
claim=ISC-4
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

CHECK="
SELECT d.id, ap.id, ap.frozen_at, er.id, er.observed_at
FROM assessor_deliveries d
JOIN audit_periods ap ON ap.id = d.audit_period_id
CROSS JOIN LATERAL jsonb_array_elements(d.manifest) m
JOIN evidence_records er ON er.id::text = m->>'evidence_record_id'
WHERE ap.frozen_at IS NOT NULL AND er.observed_at > ap.frozen_at
ORDER BY d.id, er.id;"

# A delivery of the frozen period that carries a record observed after it froze.
VIOLATION="
INSERT INTO assessor_deliveries (id, tenant_id, destination_id, audit_period_id, endpoint, status, manifest)
VALUES ('00000000-0000-0000-0000-000000000014', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-0000000000f1',
        'https://intake.example.test/v1', 'delivered',
        '[{\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e5\", \"content_hash\": \"hash-e5\"}]');"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION"
dl_require_tables "$name" "$claim" \
  "There is no delivery record, so nothing checks a departure's evidence against its period's frozen_at at the moment it leaves." \
  assessor_deliveries audit_periods evidence_records
dl_check "$name" "$claim" "$CHECK" "delivery_id	audit_period_id	frozen_at	evidence_record_id	observed_at"

echo "$name: ok -- no delivered record was observed after its frozen period's freeze"
exit 0
