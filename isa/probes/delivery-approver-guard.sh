#!/usr/bin/env bash
#
# delivery-approver-guard.sh -- falsifier for isa/assessor-delivery.md ISC-3:
# "No artifact reaches an assessor without a recorded human approver."
# Falsifier: a delivered record carrying `ai_assisted=true` whose approver is
# null, or an assembly path that skips the guard.
#
# Two checks:
#   1. Rows. No `assessor_deliveries` row is ai_assisted without a human
#      approval and a non-empty approver. An unapproved AI-assisted departure
#      counts as well as an approved one with no name: either way the artifact
#      reached the assessor with no recorded human behind it.
#   2. Schema. `assessor_deliveries` carries a CHECK that calls the shared
#      ai_assist_human_approver_guard (C11, slice 498). Without it an approval
#      can be recorded with no approver.
#
# What this does not reach: the assembly path. "An assembly path that skips
# the guard" is a property of the delivery code, which does not exist; when it
# lands, this probe needs a behavioral half that drives an unapproved draft at
# the delivery path, the way isa/probes/human-approver-guard.sh drives the
# questionnaire export.
#
# Adopted from the engine draft (#1627), which checked only a null approver
# and let an unapproved AI-assisted row through.
#
# Today: no `assessor_deliveries` table, so there is no delivery for a guard
# to sit on. FALSE.
#
# Exit 0  no AI-assisted departure lacks a recorded approver, and the guard is
#         on the table.
# Exit 1  one does, the guard is missing, or the table does not exist.
# Exit 3  cannot run (see isa/probes/_delivery-lib.sh), or the ledger is empty.
set -uo pipefail

name=delivery-approver-guard
claim=ISC-3
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

CHECK="
SELECT id, tenant_id, status, human_approved, coalesce(human_approver, '<null>')
FROM assessor_deliveries
WHERE ai_assisted
  AND (NOT human_approved OR human_approver IS NULL OR length(btrim(human_approver)) = 0)
ORDER BY id;"

# An AI-assisted departure nobody approved. The shared guard allows this row
# (it only forbids approved-without-approver), so only the row check stops it.
VIOLATION="
INSERT INTO assessor_deliveries (id, tenant_id, destination_id, endpoint, status, ai_assisted)
VALUES ('00000000-0000-0000-0000-000000000013', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000d1', 'https://intake.example.test/v1', 'delivered', true);"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION"
dl_require_tables "$name" "$claim" \
  "There is no delivery record for the approver guard to sit on, so nothing gates an artifact at the moment it leaves." \
  assessor_deliveries
dl_check "$name" "$claim" "$CHECK" "delivery_id	tenant_id	status	human_approved	human_approver"

if ! dl_run -c "
  SELECT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'public.assessor_deliveries'::regclass
      AND contype = 'c' AND convalidated
      AND pg_get_constraintdef(oid) LIKE '%ai_assist_human_approver_guard%')"; then
  echo "$name: CANNOT RUN -- constraint lookup failed: $DL_ERR" >&2
  exit 3
fi
if [[ "$DL_OUT" != "t" ]]; then
  echo "$name: FALSE ($claim) -- assessor_deliveries has no validated CHECK calling ai_assist_human_approver_guard, so an approval can be recorded with no approver"
  exit 1
fi
dl_require_rows "$name" assessor_deliveries

echo "$name: ok -- no AI-assisted departure lacks a recorded approver, and the shared guard is on assessor_deliveries"
exit 0
