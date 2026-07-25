#!/usr/bin/env bash
#
# check-status-drift.sh — fail if a slice merged on `main` is not reflected in _STATUS.md.
#
# Mirrors the repo's other generated-artifact gates (check-openapi-drift.sh,
# check-config-reference-drift.sh): regenerate from ground truth and compare.
#
# DETERMINISTIC by design — it compares only the git+events-derived MERGED set. Open PRs
# and local branches are environment-dependent (a CI checkout has neither the local
# branches nor, without auth, the PR list), so they are excluded: a merged slice is proven
# by a `type(scope): slice NNN` commit on the current ref plus committed `_events.jsonl`,
# both of which are identical in CI and locally. This keeps the gate non-flaky.
#
#   ATLAS_STATUS_FILE   committed status file (default: <repo>/docs/issues/_STATUS.md)
#
# Run: bash scripts/check-status-drift.sh
# Exit: 0 current, 1 drift (a merge not reflected — run `just status`), 2 env error.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STATUS="${ATLAS_STATUS_FILE:-$ROOT/docs/issues/_STATUS.md}"

if [ ! -f "$STATUS" ]; then
  echo "check-status-drift: status file not found: $STATUS" >&2
  exit 2
fi

# merged slice numbers from a rendered _STATUS.md "All slices" table (state column == merged)
merged_set() {
  LC_ALL=C awk -F'|' '
    { s=$2; st=$4; gsub(/ /,"",s); gsub(/ /,"",st)
      if (st=="merged" && s ~ /^[0-9]+$/) print s+0 }' | sort -n
}

committed="$(merged_set < "$STATUS")"
fresh="$(ATLAS_OPEN_PRS_FILE=/dev/null ATLAS_BRANCHES_FILE=/dev/null \
  bash "$ROOT/scripts/gen-status.sh" --stdout | merged_set)"

if [ "$committed" = "$fresh" ]; then
  echo "check-status-drift: merged-set current"
else
  echo "check-status-drift: DRIFT — a merge on main is not reflected in _STATUS.md; run 'just status'" >&2
  diff <(echo "$committed") <(echo "$fresh") || true
  exit 1
fi
