#!/usr/bin/env bash
#
# delivery-tenant-isolation.sh -- falsifier for isa/assessor-delivery.md
# ISC-7: "A delivery carries exactly one tenant's evidence." Falsifier: a
# delivered bundle containing a record whose tenant differs from the
# destination's.
#
# The check: for every delivery, the delivery row and every record in its
# manifest belong to the destination's tenant. Run as a role that sees every
# tenant (the preflight in isa/probes/_delivery-lib.sh refuses any other), so
# RLS cannot hide the very row that would falsify the claim.
#
# Three things count as violations because they leave the tenant unknown: a
# delivery with no destination (NULL or unresolved `destination_id`), and a
# manifest element that does not resolve to an `evidence_records` row. The
# last stops a manifest written with other keys or bare ids from dropping out
# of the join and passing having checked nothing.
#
# Adopted from the engine draft (#1631) in part. The draft instead attacked
# `sample_evidence`, whose evidence_record_id foreign key is single-column, so
# a tenant-A session can insert a sample row naming tenant B's record id. That
# is a real gap in the sampling tables, but it is not this claim's falsifier:
# it stops at an id and shows no B evidence reaching anything delivered. It
# was raised as a separate finding (security-atlas#1692's PR) rather than
# folded in here.
#
# Today: no delivery tables, so nothing ties a departure to one tenant. FALSE.
#
# Exit 0  every delivery carries only its destination's tenant's evidence.
# Exit 1  one carries another tenant's or an unknown tenant's, or the tables
#         do not exist.
# Exit 3  cannot run (see isa/probes/_delivery-lib.sh), the ledger is empty, or
#         no delivery carries a record.
set -uo pipefail

name=delivery-tenant-isolation
claim=ISC-7
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

CHECK="
SELECT d.id,
       coalesce(dest.tenant_id::text, '<no destination>'),
       d.tenant_id,
       coalesce(m::text, '<no element>'),
       coalesce(er.tenant_id::text, CASE WHEN m IS NULL THEN '<none>' ELSE '<unresolved>' END)
FROM assessor_deliveries d
LEFT JOIN assessor_destinations dest ON dest.id = d.destination_id
LEFT JOIN LATERAL jsonb_array_elements(d.manifest) m ON true
LEFT JOIN evidence_records er ON er.id::text = m->>'evidence_record_id'
WHERE dest.id IS NULL
   OR d.tenant_id <> dest.tenant_id
   OR (m IS NOT NULL AND (er.id IS NULL OR er.tenant_id <> dest.tenant_id))
ORDER BY 1, 4;"

# Tenant A's delivery carries tenant B's record.
VIOLATION="
INSERT INTO assessor_deliveries (id, tenant_id, destination_id, endpoint, status, manifest)
VALUES ('00000000-0000-0000-0000-000000000017', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000d1', 'https://intake.example.test/v1', 'delivered',
        '[{\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e1\", \"content_hash\": \"hash-e1\"},
          {\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e9\", \"content_hash\": \"hash-e9\"}]');"

# The same tenant B record, as a bare id the check's key does not read.
DRIFT="
INSERT INTO assessor_deliveries (id, tenant_id, destination_id, endpoint, status, manifest)
VALUES ('00000000-0000-0000-0000-000000000018', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000d1', 'https://intake.example.test/v1', 'delivered',
        '[\"00000000-0000-0000-0000-0000000000e9\"]');"

# At least one delivered record, or the clean result has checked nothing. An
# individual empty manifest is allowed.
SCOPE="
SELECT EXISTS (
  SELECT 1 FROM assessor_deliveries d CROSS JOIN LATERAL jsonb_array_elements(d.manifest) m);"
EMPTY_MANIFESTS="UPDATE assessor_deliveries SET manifest = '[]';"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION" "$DRIFT"
dl_selftest_scope "$name" "$SCOPE" "$EMPTY_MANIFESTS"
dl_require_tables "$name" "$claim" \
  "There is no delivery record binding a departure to a destination's tenant, so nothing establishes that what left was one tenant's evidence." \
  assessor_destinations assessor_deliveries evidence_records
dl_check "$name" "$claim" "$CHECK" "delivery_id	destination_tenant	delivery_tenant	manifest_element	record_tenant"
dl_require_rows "$name" assessor_deliveries
dl_require_scope "$name" "$SCOPE" "delivered record"

echo "$name: ok -- every delivery carries only its destination's tenant's evidence"
exit 0
