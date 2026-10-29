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
# Exit 1  one carries another tenant's, or the tables do not exist.
# Exit 3  cannot run (see isa/probes/_delivery-lib.sh).
set -uo pipefail

name=delivery-tenant-isolation
claim=ISC-7
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

CHECK="
SELECT d.id, dest.tenant_id, d.tenant_id, coalesce(er.id::text, '<none>'), coalesce(er.tenant_id::text, '<none>')
FROM assessor_deliveries d
JOIN assessor_destinations dest ON dest.id = d.destination_id
LEFT JOIN LATERAL (
  SELECT e.id, e.tenant_id
  FROM jsonb_array_elements(d.manifest) m
  JOIN evidence_records e ON e.id::text = m->>'evidence_record_id'
  WHERE e.tenant_id <> dest.tenant_id) er ON true
WHERE d.tenant_id <> dest.tenant_id OR er.id IS NOT NULL
ORDER BY d.id;"

# Tenant A's delivery carries tenant B's record.
VIOLATION="
INSERT INTO assessor_deliveries (id, tenant_id, destination_id, endpoint, status, manifest)
VALUES ('00000000-0000-0000-0000-000000000017', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000d1', 'https://intake.example.test/v1', 'delivered',
        '[{\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e1\", \"content_hash\": \"hash-e1\"},
          {\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e9\", \"content_hash\": \"hash-e9\"}]');"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION"
dl_require_tables "$name" "$claim" \
  "There is no delivery record binding a departure to a destination's tenant, so nothing establishes that what left was one tenant's evidence." \
  assessor_destinations assessor_deliveries evidence_records
dl_check "$name" "$claim" "$CHECK" "delivery_id	destination_tenant	delivery_tenant	evidence_record_id	record_tenant"

echo "$name: ok -- every delivery carries only its destination's tenant's evidence"
exit 0
