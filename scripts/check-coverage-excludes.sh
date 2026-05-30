#!/usr/bin/env bash
#
# check-coverage-excludes.sh — slice 353 (Q-5 from slice 333's QA audit)
#
# Assert that every prefix listed in `excludes` of
# cmd/scripts/coverage-thresholds.json has a matching written
# justification under `$exclude_justifications`.
#
# Background (slice 333 Theme 2, finding Q-5): the coverage-gate's
# `excludes` list is the path of least resistance for landing a slice
# without writing tests. The list trends monotonically up over a
# project's lifetime and each entry's rationale is buried in a slice's
# commit history. This guard makes an unjustified exclude un-mergeable:
# adding a prefix to `excludes` without a matching `$exclude_justifications`
# entry fails the check. It is the structural analog of slice 345's
# integration-enrolment guard — a hand-maintained list, derived-checked.
#
# The check is intentionally NOT a coverage gate (that is
# cmd/scripts/coverage-gate). It only asserts the documentation
# invariant: excludes ⊆ exclude_justifications AND
# exclude_justifications ⊆ excludes (no orphan justifications either,
# so the map cannot drift stale). Both directions are enforced so the
# map stays a faithful mirror of the live exclude set.
#
# Exit codes:
#   0 — every exclude has a non-empty justification AND no orphan
#       justification exists
#   1 — at least one exclude lacks a justification, a justification is
#       empty, or an orphan justification exists
#   2 — environment misconfigured (jq missing, thresholds file
#       unreadable, JSON malformed)
#
# Usage:
#   bash scripts/check-coverage-excludes.sh
#   COVERAGE_THRESHOLDS=/path/to/file.json bash scripts/check-coverage-excludes.sh
#
# Local repro / self-test:
#   bash scripts/check-coverage-excludes_test.sh

set -euo pipefail

THRESHOLDS="${COVERAGE_THRESHOLDS:-cmd/scripts/coverage-thresholds.json}"

if ! command -v jq >/dev/null 2>&1; then
  echo "check-coverage-excludes: jq not found on PATH" >&2
  exit 2
fi

if [[ ! -r "$THRESHOLDS" ]]; then
  echo "check-coverage-excludes: thresholds file not readable: $THRESHOLDS" >&2
  exit 2
fi

if ! jq -e . "$THRESHOLDS" >/dev/null 2>&1; then
  echo "check-coverage-excludes: thresholds file is not valid JSON: $THRESHOLDS" >&2
  exit 2
fi

# Excludes present but no $exclude_justifications block at all is a misconfig.
if ! jq -e 'has("excludes")' "$THRESHOLDS" >/dev/null 2>&1; then
  echo "check-coverage-excludes: no \"excludes\" array in $THRESHOLDS" >&2
  exit 2
fi
if ! jq -e 'has("$exclude_justifications")' "$THRESHOLDS" >/dev/null 2>&1; then
  echo "check-coverage-excludes: no \"\$exclude_justifications\" object in $THRESHOLDS" >&2
  echo "  Slice 353 (Q-5) requires every exclude to carry a written justification." >&2
  exit 2
fi

fail=0

# 1. Every exclude must have a non-empty justification.
missing="$(jq -r '
  .excludes[] as $e
  | select((."$exclude_justifications"[$e] // "") | (type != "string") or (. | length == 0))
  | $e
' "$THRESHOLDS")"

if [[ -n "$missing" ]]; then
  fail=1
  echo "check-coverage-excludes: the following excludes lack a non-empty \$exclude_justifications entry:" >&2
  while IFS= read -r e; do
    [[ -n "$e" ]] && echo "  - $e" >&2
  done <<< "$missing"
fi

# 2. No orphan justification (a justification for a prefix no longer excluded).
orphans="$(jq -r '
  (."$exclude_justifications" | keys[]) as $k
  | select(($k | startswith("$")) | not)
  | select((.excludes | index($k)) == null)
  | $k
' "$THRESHOLDS")"

if [[ -n "$orphans" ]]; then
  fail=1
  echo "check-coverage-excludes: the following \$exclude_justifications entries are orphans (no matching exclude):" >&2
  while IFS= read -r k; do
    [[ -n "$k" ]] && echo "  - $k" >&2
  done <<< "$orphans"
fi

if (( fail != 0 )); then
  echo "" >&2
  echo "Fix: every exclude in $THRESHOLDS must have a matching, non-empty key in" >&2
  echo "  \$exclude_justifications, and vice versa. See slice 353 / slice 333 Q-5." >&2
  exit 1
fi

count="$(jq -r '.excludes | length' "$THRESHOLDS")"
echo "check-coverage-excludes: OK — all $count excludes carry a justification; no orphans."
