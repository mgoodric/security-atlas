#!/usr/bin/env bash
#
# delivery-registered-only.sh -- falsifier for isa/assessor-delivery.md ISC-1:
# "Evidence leaves only through a registered destination." Falsifier: a
# delivery whose endpoint was supplied by the request rather than read from an
# `assessor_destinations` row.
#
# The check: every `assessor_deliveries` row names a same-tenant
# `assessor_destinations` row, and the endpoint it recorded as dialed is that
# row's endpoint. A delivery with no destination, a destination in another
# tenant, or an endpoint that differs from the registered one is a request
# supplying its own address. Destination endpoints are immutable in the
# assumed schema (isa/probes/_delivery-lib.sh), so a past delivery is compared
# with the endpoint it was actually registered under.
#
# Adopted from the engine draft (#1625), which joined on endpoint text alone;
# that let a delivery match any destination that happened to share the URL.
#
# Today: the tables do not exist, and the only egress -- the OSCAL bundle
# download -- goes wherever the caller takes it. FALSE.
#
# Exit 0  every delivery went to the registered endpoint of its destination.
# Exit 1  a delivery did not, or the tables do not exist.
# Exit 3  cannot run: no psql or DATABASE_URL, a role that cannot see every
#         tenant, an unmigrated database, a failed self-test, a schema that
#         differs from the assumed one, or no delivery rows to check.
set -uo pipefail

name=delivery-registered-only
claim=ISC-1
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

CHECK="
SELECT d.id, d.tenant_id, d.endpoint, coalesce(dest.endpoint, '<no registered destination>')
FROM assessor_deliveries d
LEFT JOIN assessor_destinations dest
  ON dest.id = d.destination_id AND dest.tenant_id = d.tenant_id
WHERE dest.id IS NULL OR dest.endpoint IS DISTINCT FROM d.endpoint
ORDER BY d.id;"

# A delivery the request addressed itself: no destination row behind it.
VIOLATION="
INSERT INTO assessor_deliveries (id, tenant_id, endpoint, status)
VALUES ('00000000-0000-0000-0000-000000000012', '00000000-0000-0000-0000-0000000000a1',
        'https://caller-chosen.example.test/upload', 'delivered');"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION"
dl_require_tables "$name" "$claim" \
  "There is no registered-destination store. The one way evidence leaves today, POST /v1/audit-periods/{id}/oscal-export:download (internal/api/oscalexport), hands the signed bundle to whichever caller asks, and the caller decides where it goes." \
  assessor_destinations assessor_deliveries
dl_check "$name" "$claim" "$CHECK" "delivery_id	tenant_id	endpoint_dialed	registered_endpoint"
dl_require_rows "$name" assessor_deliveries

echo "$name: ok -- every delivery went to the registered endpoint of a same-tenant destination"
exit 0
