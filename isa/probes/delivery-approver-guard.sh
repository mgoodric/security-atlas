#!/usr/bin/env bash
#
# delivery-approver-guard.sh — falsifier for isa/assessor-delivery.md ISC-3.
#
# ISC-3: "No artifact reaches an assessor without a recorded human approver."
# Falsifier (as stated in the claim): a delivered record carrying
# `ai_assisted=true` whose approver is null, OR an assembly path that skips
# the guard entirely.
#
# This probe checks the second disjunct first, because it is the one that is
# true TODAY. `assessor_deliveries` — the table the epic's own Test Strategy
# names as the record of what left the platform — does not exist anywhere in
# `migrations/sql/`. There is therefore no schema-level guard for delivered
# records to adopt, which means the current state of the repo is exactly
# "an assembly path that skips the guard": nothing stops an ai_assisted,
# unapproved record from being treated as delivered, because "delivered" has
# no schema representation to gate at all. That is a genuine falsification of
# ISC-3, not an unrunnable probe — the missing piece is the feature under
# test, not the ability to test it.
#
# Once `assessor_deliveries` ships adopting the shared
# `ai_assist_human_approver_guard(ai_assisted, human_approved, human_approver)`
# CHECK template (CLAUDE.md "ai_assisted/human_approved guard"), this probe
# additionally verifies the guard is present and that no row already violates
# it (the first disjunct of the falsifier), so it keeps working as the
# claim's falsifier rather than needing to be rewritten at that point.
#
# Required env:
#   DATABASE_URL — connection string to a role with full pg_catalog
#                  visibility (atlas_migrate; BYPASSRLS recommended, same
#                  requirement as scripts/audit-rls.sh). This is a
#                  cross-tenant audit, not an RLS-conformance check, so the
#                  migrate role is correct here, not DATABASE_URL_APP.
#
# Exit codes (the ISA falsifier contract — deliberately three-way, unlike
# audit-rls.sh's exit-2 "environment misconfigured"):
#   0 — assessor_deliveries exists, carries the guard, and no row violates it
#   1 — the claim does not hold: no guard exists, or a row violates it
#   3 — cannot run: no DATABASE_URL, no psql, or Postgres unreachable

set -Eeuo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "delivery-approver-guard: DATABASE_URL is not set — cannot run (bring up the harness: just db-up && just migrate-up, then export DATABASE_URL)" >&2
  exit 3
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "delivery-approver-guard: psql not on PATH — cannot run" >&2
  exit 3
fi

if ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -c "SELECT 1" >/dev/null 2>&1; then
  echo "delivery-approver-guard: cannot reach Postgres at DATABASE_URL — cannot run" >&2
  exit 3
fi

table_exists="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -c \
  "SELECT (to_regclass('public.assessor_deliveries') IS NOT NULL)")"

if [[ "$table_exists" != "t" ]]; then
  echo "delivery-approver-guard: FAIL — no assessor_deliveries table exists in this schema." >&2
  echo "ISC-3 requires every delivered record to carry a schema-enforced ai_assisted -> human_approver guard." >&2
  echo "There is no delivery table for such a guard to attach to, so the (nonexistent) assembly path trivially skips it: nothing in the codebase stops an ai_assisted, unapproved record from being marked delivered." >&2
  exit 1
fi

guard_present="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -c "
  SELECT count(*) > 0
  FROM pg_constraint con
  JOIN pg_class c ON c.oid = con.conrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
    AND c.relname = 'assessor_deliveries'
    AND con.contype = 'c'
    AND pg_get_constraintdef(con.oid) ILIKE '%ai_assist_human_approver_guard%'
")"

if [[ "$guard_present" != "t" ]]; then
  echo "delivery-approver-guard: FAIL — assessor_deliveries exists but carries no CHECK constraint invoking ai_assist_human_approver_guard." >&2
  echo "Nothing at the schema level stops an ai_assisted delivered row from having a null human_approver." >&2
  exit 1
fi

violations="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -A -t -c "
  SELECT count(*)
  FROM assessor_deliveries
  WHERE ai_assisted = true
    AND (human_approver IS NULL OR length(human_approver) = 0)
")"

if [[ "$violations" -gt 0 ]]; then
  echo "delivery-approver-guard: FAIL — $violations delivered record(s) carry ai_assisted=true with no recorded human approver, despite the guard constraint existing." >&2
  exit 1
fi

echo "delivery-approver-guard: ok — assessor_deliveries exists, adopts ai_assist_human_approver_guard, and no delivered record violates it."
exit 0
