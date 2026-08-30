#!/usr/bin/env bash
#
# delivery-registered-only.sh — falsifier for isa/assessor-delivery.md ISC-1:
# "Evidence leaves only through a registered destination." Falsifier: a
# delivery whose endpoint was supplied by the request rather than read from
# an `assessor_destinations` row.
#
# The check has two phases, because the feature it guards has not shipped:
#
#   Phase 1 (today). `assessor_destinations` does not exist as a table, so
#   there is no registered-destination store for anything to read an
#   endpoint from. The one evidence-egress path that exists in the tree —
#   POST /v1/audit-periods/{id}/oscal-export:download
#   (internal/api/oscalexport) — hands the signed, audit-binding bundle to
#   whichever caller holds a valid session token; the destination is decided
#   by the request making the call, not looked up from a row the platform
#   owns. That is the exact shape ISC-1 forbids, so this phase reports the
#   claim FALSE (exit 1). It is not a missing fixture: the query that decides
#   this (to_regclass against the catalog) always runs cleanly, connection or
#   not; the absence of the table is itself the defect.
#
#   Phase 2 (once assessor_destinations + assessor_deliveries ship). Every
#   row in assessor_deliveries must resolve to a same-tenant row in
#   assessor_destinations by endpoint. A delivery row whose endpoint has no
#   match means the request supplied the endpoint directly — the falsifier
#   this claim names, now checked against real data instead of by the
#   schema's absence.
#
# Required env:
#   DATABASE_URL — full-catalog-visibility role (atlas_migrate / BYPASSRLS),
#                  same role scripts/audit-rls.sh uses. This is a
#                  cross-tenant catalog audit, not an RLS-bound app-role
#                  check: a violation in any tenant's rows falsifies the
#                  claim.
#
# Exit codes:
#   0 — every delivery's endpoint resolves to a registered destination row
#   1 — the claim does not hold (see phases above)
#   3 — cannot run: no DATABASE_URL, no psql, or the database is unreachable

set -Eeuo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "delivery-registered-only: DATABASE_URL is not set" >&2
  exit 3
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "delivery-registered-only: psql not on PATH" >&2
  exit 3
fi

if ! psql "$DATABASE_URL" -Atqc "SELECT 1" >/dev/null 2>&1; then
  echo "delivery-registered-only: cannot connect via DATABASE_URL" >&2
  exit 3
fi

has_destinations="$(psql "$DATABASE_URL" -Atqc "SELECT to_regclass('public.assessor_destinations') IS NOT NULL;")"

if [[ "$has_destinations" != "t" ]]; then
  cat >&2 <<'MSG'
delivery-registered-only: FALSE (ISC-1) — no assessor_destinations table
exists. There is no registered-destination store for evidence to leave
through. The only evidence-egress path in the tree today
(POST /v1/audit-periods/{id}/oscal-export:download, internal/api/oscalexport)
returns the signed bundle to whichever caller holds a valid session token —
the destination is decided by the request, not read from a row the platform
controls. Evidence leaves through a caller-supplied endpoint, not a
registered destination.
MSG
  exit 1
fi

has_deliveries="$(psql "$DATABASE_URL" -Atqc "SELECT to_regclass('public.assessor_deliveries') IS NOT NULL;")"

if [[ "$has_deliveries" != "t" ]]; then
  echo "delivery-registered-only: FALSE (ISC-1) — assessor_destinations exists but assessor_deliveries does not, so no delivery is recorded as having gone through it." >&2
  exit 1
fi

orphans="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -F $'\t' -c "
  SELECT d.id, d.tenant_id, d.endpoint
  FROM assessor_deliveries d
  LEFT JOIN assessor_destinations dest
    ON dest.endpoint = d.endpoint
   AND dest.tenant_id = d.tenant_id
  WHERE dest.id IS NULL;
")"

if [[ -n "$orphans" ]]; then
  echo "delivery-registered-only: FALSE (ISC-1) — delivery row(s) whose endpoint is absent from assessor_destinations:" >&2
  echo "delivery_id	tenant_id	endpoint" >&2
  printf '%s\n' "$orphans" >&2
  exit 1
fi

echo "delivery-registered-only: ok — every delivery's endpoint resolves to a registered assessor_destinations row"
exit 0
