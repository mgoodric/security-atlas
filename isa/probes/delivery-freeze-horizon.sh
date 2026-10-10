#!/usr/bin/env bash
#
# delivery-freeze-horizon.sh — falsifier for isa/assessor-delivery.md ISC-4:
# "A frozen period delivers nothing observed after its freeze." Falsifier: a
# delivered record whose `observed_at` is later than the period's
# `frozen_at`.
#
# The check has two phases, because the feature it guards has not shipped —
# same shape as the sibling D1/D2 probes (delivery-registered-only.sh,
# delivery-replay.sh, delivery-approver-guard.sh):
#
#   Phase 1 (today). `assessor_deliveries` does not exist as a table, so
#   there is no schema location that records what left, when, or which
#   evidence it carried. That means the horizon has no delivery-time gate at
#   all: nothing stops a caller from delivering a payload built from
#   `evidence_records` with `observed_at` after the period's `frozen_at`,
#   because there is no "delivered" concept to check it against.
#
#   This is not merely a hypothetical absence. The one evidence-egress path
#   that exists in the tree today — POST
#   /v1/audit-periods/{id}/oscal-export:download (internal/api/oscalexport)
#   — does bound the sample-population evidence it draws (internal/oscal
#   Aggregate reads sample_evidence rows materialized at draw-time by
#   ListPopulationEvidenceIDs, which filters `observed_at <= frozen_at`
#   before persisting the draw). But two other reads in that same
#   aggregation (internal/oscal/aggregate.go: ListActiveControlsWithDescription
#   and ListPolicies, steps 3-4) are unbounded LIVE reads with no horizon
#   filter of any kind, and the walkthrough/audit-note reads
#   (ListWalkthroughsForPeriod, ListAuditNotesForPeriod,
#   ListWalkthroughAttachmentsForPeriod — internal/db/queries/oscal_export.sql)
#   are scoped only by `audit_period_id`, with no `created_at <= frozen_at`
#   clause at all. So even the one enforcement point that exists is partial,
#   and there is no delivery-specific gate sitting downstream of it to catch
#   what that aggregation misses. That is ISC-4 failing today, not an
#   unrunnable probe — the missing piece is the feature under test, not the
#   ability to test it.
#
#   Phase 2 (once assessor_deliveries ships). Every row must join to an
#   evidence_records row whose observed_at does not exceed the audit_periods
#   row's frozen_at for the period the delivery names. A delivery whose
#   evidence observed_at is later than its period's frozen_at is the
#   falsifier this claim names, now checked against real data instead of by
#   the schema's absence.
#
# Schema assumption for phase 2 (not yet built — D2 is unimplemented): one
# assessor_deliveries row per delivered evidence record, columns
# `evidence_record_id` (FK -> evidence_records.id) and `audit_period_id`
# (FK -> audit_periods.id) — mirroring the delivery-replay.sh assumption for
# ISC-2. If D2 lands a different shape (e.g. a whole-bundle row with a
# manifest of record ids), this query is the first thing to reconcile
# against the real migration, not the falsifier's intent.
#
# Required env:
#   DATABASE_URL — full-catalog-visibility role (atlas_migrate / BYPASSRLS),
#                  same role scripts/audit-rls.sh and the sibling delivery
#                  probes use. This is a cross-tenant catalog audit: a
#                  violation in any tenant's rows falsifies the claim.
#
# Exit codes:
#   0 — every delivered record's observed_at is at or before its period's
#       frozen_at
#   1 — the claim does not hold (see phases above)
#   3 — cannot run: no DATABASE_URL, no psql, or the database is unreachable

set -Eeuo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "delivery-freeze-horizon: DATABASE_URL is not set" >&2
  exit 3
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "delivery-freeze-horizon: psql not on PATH" >&2
  exit 3
fi

if ! psql "$DATABASE_URL" -Atqc "SELECT 1" >/dev/null 2>&1; then
  echo "delivery-freeze-horizon: cannot connect via DATABASE_URL" >&2
  exit 3
fi

has_deliveries="$(psql "$DATABASE_URL" -Atqc "SELECT to_regclass('public.assessor_deliveries') IS NOT NULL;")"

if [[ "$has_deliveries" != "t" ]]; then
  cat >&2 <<'MSG'
delivery-freeze-horizon: FALSE (ISC-4) — no assessor_deliveries table
exists. There is no schema location recording what left, when, or which
evidence a departure carried, so nothing enforces "observed_at <= frozen_at"
at the moment of delivery. The one enforcement point that exists today lives
inside the OSCAL export aggregation (internal/oscal/aggregate.go), and it is
partial: sample-population evidence is bounded correctly (drawn at
draw-time under the frozen horizon), but the active-controls and policies
reads in that same aggregation are unbounded live reads, and the
walkthrough/audit-note reads carry no created_at horizon filter at all
(internal/db/queries/oscal_export.sql). A frozen period has no
delivery-time gate that stops evidence observed after its freeze from
leaving.
MSG
  exit 1
fi

has_periods="$(psql "$DATABASE_URL" -Atqc "SELECT to_regclass('public.audit_periods') IS NOT NULL;")"

if [[ "$has_periods" != "t" ]]; then
  echo "delivery-freeze-horizon: FALSE (ISC-4) — assessor_deliveries exists but audit_periods does not, so no delivered record can be checked against a freeze horizon at all." >&2
  exit 1
fi

violations="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -F $'\t' -c "
  SELECT ad.id, ad.tenant_id, er.id, er.observed_at, ap.id, ap.frozen_at
  FROM assessor_deliveries ad
  JOIN evidence_records er
    ON er.tenant_id = ad.tenant_id AND er.id = ad.evidence_record_id
  JOIN audit_periods ap
    ON ap.tenant_id = ad.tenant_id AND ap.id = ad.audit_period_id
  WHERE ap.frozen_at IS NOT NULL
    AND er.observed_at > ap.frozen_at;
")"

if [[ -n "$violations" ]]; then
  echo "delivery-freeze-horizon: FALSE (ISC-4) — delivered record(s) whose observed_at is later than the period's frozen_at:" >&2
  echo "delivery_id	tenant_id	evidence_record_id	observed_at	audit_period_id	frozen_at" >&2
  printf '%s\n' "$violations" >&2
  exit 1
fi

echo "delivery-freeze-horizon: ok — every delivered record's observed_at is at or before its period's frozen_at"
exit 0
