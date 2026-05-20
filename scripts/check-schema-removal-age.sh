#!/usr/bin/env bash
#
# check-schema-removal-age.sh — enforce the 90-day deprecation window
# for breaking-major bumps of `evidence_kind` JSON Schemas.
#
# Slice 179 — implementation half of the OQ #9 + #17 resolution
# (canvas/11-open-questions.md, resolved 2026-05-20). Governance rule
# lives in CONTRIBUTING.md "Contributing an `evidence_kind` schema":
# when a v2.0.0 lands, v1.x.x must stay in the registry for at least
# 90 days before removal. This script is the structural enforcer.
#
# Inputs:
#   - List of removed schema-version files (paths) on stdin, one per
#     line, OR as positional arguments. Empty input -> no removals
#     -> exit 0.
#
# For each removed path:
#   1. Reads its introduction date on `main` via
#        git log --diff-filter=A --format=%cI -- <path>
#      (TRUST ROOT — P0-179-1: ONLY git history on main, never a
#      PR-mutable source like filename / frontmatter / arg.)
#   2. Computes age = now - introduction_date (in days).
#   3. Floor: 90 days. If age >= 90 the removal is eligible; if not,
#      emits the AC-3 error message and queues a non-zero exit.
#
# Env:
#   SCHEMA_REMOVAL_OVERRIDE=1
#     Bypass all violations. Set by the CI job ONLY when the PR carries
#     the `[deprecation-override]` label (exact spelling — P0-179-2).
#     When bypass is active and violations are found, the script prints
#     them to stderr for the audit trail, then exits 0.
#
#   SCHEMA_REMOVAL_MAIN_REF
#     Override the git ref used for the introduction-date lookup.
#     Default: `main`. Used by the test harness so the integration
#     fixtures don't need a literal `main` branch.
#
#   SCHEMA_REMOVAL_NOW
#     Override "now" as an ISO-8601 UTC timestamp (e.g.
#     `2026-05-20T00:00:00Z`). Default: current UTC time. Used by the
#     test harness for deterministic age arithmetic.
#
# Exit codes:
#   0  All removals satisfy the 90-day floor (OR override is set OR
#      no removals were supplied).
#   1  At least one removal violates the floor and override is unset.
#   2  Internal usage / configuration error (missing dep, bad arg,
#      `git log` returned nothing for a path).
#
# Required deps: git, python3.
#
# Discipline:
#   - P0-179-1: introduction date read ONLY from `git log` on
#     `${SCHEMA_REMOVAL_MAIN_REF:-main}`.
#   - P0-179-2: override env var is binary; the override LABEL name is
#     not embedded in this script (the CI job is the label-aware
#     surface; see `.github/workflows/ci.yml::schema-removal-age`).
#   - P0-179-6: edge cases (empty input, file absent from main,
#     malformed git output) exit with a clear stderr message and a
#     non-panic code (2 for misuse, 1 for genuine floor violation,
#     never an uncaught error).
#
# Local repro (against the actual PR branch):
#   git diff --diff-filter=D --name-only origin/main...HEAD \
#     -- internal/api/schemaregistry/schemas/ \
#     | bash scripts/check-schema-removal-age.sh
#
# Test harness: scripts/check-schema-removal-age_test.sh

set -eu

FLOOR_DAYS=90
MAIN_REF="${SCHEMA_REMOVAL_MAIN_REF:-main}"
OVERRIDE="${SCHEMA_REMOVAL_OVERRIDE:-0}"

if ! command -v git >/dev/null 2>&1; then
  echo "check-schema-removal-age: 'git' not found on PATH" >&2
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "check-schema-removal-age: 'python3' not found on PATH" >&2
  exit 2
fi

# Build the list of paths to evaluate. Positional args take precedence
# over stdin; if neither, the input list is empty.
paths=()
if [[ $# -gt 0 ]]; then
  paths=("$@")
elif ! [ -t 0 ]; then
  while IFS= read -r line; do
    # Skip blank lines.
    [[ -z "$line" ]] && continue
    paths+=("$line")
  done
fi

# AC-4 + AC-10(d): zero removals = quiet pass.
if [[ ${#paths[@]} -eq 0 ]]; then
  echo "check-schema-removal-age: no removed schema files supplied; nothing to check."
  exit 0
fi

# Resolve "now" once for deterministic arithmetic across all paths in
# one invocation.
now_iso="${SCHEMA_REMOVAL_NOW:-$(python3 -c 'import datetime; print(datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"))')}"

violations=0
violation_messages=()

for path in "${paths[@]}"; do
  # AC-2 + P0-179-1: read introduction-on-main commit time. We use
  # --diff-filter=A (added) so we capture the FIRST commit that
  # introduced the file. --format=%cI yields committer-date ISO-8601
  # (strict, with timezone offset). `tail -n 1` selects the OLDEST
  # match on the branch — git log defaults to reverse-chronological,
  # so the file's birth commit is last.
  intro_iso="$(git log --diff-filter=A --format=%cI "$MAIN_REF" -- "$path" 2>/dev/null | tail -n 1 || true)"

  if [[ -z "$intro_iso" ]]; then
    # P0-179-6: file not present in `main`'s history.
    # This is a genuine misuse — a removed-file list that includes a
    # path the trust-root has no record of can't be evaluated. Could
    # be: (a) the file was created and removed in the same PR (which
    # means it never landed on main and the 90-day clock never
    # started), or (b) the CI checkout doesn't have `main`'s history.
    # Either way, the safe call is to surface the condition with a
    # clear message and fail closed (exit 2) so the maintainer can
    # decide rather than the script silently passing.
    echo "check-schema-removal-age: no introduction commit on '$MAIN_REF' for '$path' — was the file ever merged to main? (If created+deleted in the same PR, drop it from the diff; if main history is shallow, deepen the checkout.)" >&2
    exit 2
  fi

  # AC-2: compute age in whole days via python3 (BSD/GNU `date`
  # divergence makes shell-only arithmetic non-portable; D3).
  # Implementation note: use `python3 -c` (one-line program) instead of
  # a heredoc so the script parses cleanly under bash 3.2 (the macOS
  # default shell). Bash 3.2's parser doesn't always reconcile a
  # heredoc body nested inside `$(...)` correctly — `python3 -c` is
  # the portable form.
  age_days="$(python3 -c '
import sys, datetime
try:
    intro = datetime.datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00"))
    now = datetime.datetime.fromisoformat(sys.argv[2].replace("Z", "+00:00"))
except (ValueError, IndexError):
    sys.exit(1)
delta = now - intro
# Whole-day floor of the elapsed interval. Partial days do not bump.
print(int(delta.total_seconds() // 86400))
' "$intro_iso" "$now_iso" 2>/dev/null || true)"

  if [[ -z "$age_days" ]] || ! [[ "$age_days" =~ ^-?[0-9]+$ ]]; then
    echo "check-schema-removal-age: failed to parse introduction date '$intro_iso' or now '$now_iso' for '$path'" >&2
    exit 2
  fi

  if (( age_days < FLOOR_DAYS )); then
    remaining=$(( FLOOR_DAYS - age_days ))
    # AC-3 — exact message shape from the slice doc.
    violation_messages+=("$path introduced $intro_iso, age $age_days days, must be >= ${FLOOR_DAYS} days; remaining: $remaining days. Override with [deprecation-override] label + audit-log entry.")
    violations=$(( violations + 1 ))
  else
    echo "ok: $path introduced $intro_iso, age $age_days days (>= ${FLOOR_DAYS}-day floor)."
  fi
done

if (( violations > 0 )); then
  echo "" >&2
  echo "check-schema-removal-age: $violations removal(s) violate the ${FLOOR_DAYS}-day deprecation window:" >&2
  for m in "${violation_messages[@]}"; do
    echo "  - $m" >&2
  done

  if [[ "$OVERRIDE" == "1" ]]; then
    # AC-5: override bypasses, but the violation list still prints
    # to stderr above. That output forms the human-readable audit
    # trail referenced by P0-179-3 (the maintainer's audit-log entry
    # under docs/audit-log/ references this output).
    echo "" >&2
    echo "check-schema-removal-age: SCHEMA_REMOVAL_OVERRIDE=1 set — bypassing failure. The PR is expected to carry the [deprecation-override] label and an entry under docs/audit-log/ documenting the rationale (P0-179-3)." >&2
    exit 0
  fi

  exit 1
fi

# AC-4: all removals satisfy the floor.
echo ""
echo "check-schema-removal-age: all ${#paths[@]} removal(s) satisfy the ${FLOOR_DAYS}-day floor."
exit 0
