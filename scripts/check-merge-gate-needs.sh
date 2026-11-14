#!/usr/bin/env bash
#
# check-merge-gate-needs.sh — #1887
#
# Fail when a job in .github/workflows/ci.yml runs on every PR (no job-level
# `if:`) but is not in merge-gate's `needs:`.
#
# merge-gate is the check that blocks a merge, and it asserts exactly the
# jobs in its `needs:`. A blocking check that runs unconditionally but sits
# outside that list reports red and blocks nothing: coverage-excludes-check,
# cache-path-guard and integration-enrolment-check all did until #1887.
# Path-filtered jobs (`if: needs.changes.outputs.code == 'true'`) are left
# to the reviewer, since some of them are deliberately advisory.
#
# ALLOWLIST names an unconditional job that is deliberately left out of
# merge-gate, with the reason. An entry fails the check when it goes stale
# (the job is gone, gains an `if:`, or is in `needs:` after all), so the
# list cannot rot into a blanket waiver.
#
# The parser reads the workflow's own layout: jobs at two spaces under
# `jobs:`, job keys at four, merge-gate's `needs:` as a block list of
# `      - <job>` lines. A layout it cannot read (no jobs, no merge-gate, an
# empty needs list) exits 2 rather than passing.
#
# Exit codes:
#   0 — every unconditional job is in merge-gate's needs or allowlisted
#   1 — at least one is not, or an allowlist entry is stale
#   2 — the workflow is unreadable or its layout is not recognised
#
# Usage:
#   bash scripts/check-merge-gate-needs.sh
#   CI_WORKFLOW=/path/to/ci.yml bash scripts/check-merge-gate-needs.sh
#
# Self-test:
#   bash scripts/check-merge-gate-needs_test.sh

set -euo pipefail

WORKFLOW="${CI_WORKFLOW:-.github/workflows/ci.yml}"
GATE="merge-gate"

# "<job> <reason>", one per line. Empty today: every unconditional job in
# ci.yml is a check whose red result should block the merge. Add an entry
# only for a job that must run on every PR yet must not block, and say why.
# MERGE_GATE_ALLOWLIST replaces it for the self-test only; ci.yml never sets it.
ALLOWLIST="${MERGE_GATE_ALLOWLIST-
}"

if [[ ! -f "$WORKFLOW" || ! -r "$WORKFLOW" ]]; then
  echo "check-merge-gate-needs: workflow not readable: $WORKFLOW" >&2
  exit 2
fi

# One line per job: "<job> <conditional|unconditional>".
jobs="$(
  awk '
    /^jobs:[[:space:]]*$/ { in_jobs = 1; next }
    in_jobs && /^[^[:space:]#]/ { in_jobs = 0 }
    !in_jobs { next }
    /^  [A-Za-z0-9_-]+:[[:space:]]*$/ {
      if (job != "") print job, (cond ? "conditional" : "unconditional")
      job = $1; sub(/:$/, "", job); cond = 0; next
    }
    /^    if:/ { cond = 1 }
    END { if (job != "") print job, (cond ? "conditional" : "unconditional") }
  ' "$WORKFLOW"
)"

# merge-gate's needs, one job per line.
needs="$(
  awk -v gate="$GATE" '
    /^jobs:[[:space:]]*$/ { in_jobs = 1; next }
    in_jobs && /^[^[:space:]#]/ { in_jobs = 0 }
    !in_jobs { next }
    /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { in_gate = ($1 == gate ":"); in_needs = 0; next }
    !in_gate { next }
    /^    needs:[[:space:]]*$/ { in_needs = 1; next }
    in_needs && /^    [A-Za-z]/ { in_needs = 0 }
    in_needs && /^      - / { j = $2; print j }
  ' "$WORKFLOW"
)"

if [[ -z "$jobs" ]]; then
  echo "check-merge-gate-needs: no jobs found under 'jobs:' in $WORKFLOW" >&2
  exit 2
fi
if ! printf '%s\n' "$jobs" | awk '{ print $1 }' | grep -qxF "$GATE"; then
  echo "check-merge-gate-needs: no '$GATE' job in $WORKFLOW" >&2
  exit 2
fi
if [[ -z "$needs" ]]; then
  echo "check-merge-gate-needs: '$GATE' has no block-list 'needs:' in $WORKFLOW" >&2
  exit 2
fi

allowlisted() { printf '%s\n' "$ALLOWLIST" | awk 'NF { print $1 }' | grep -qxF "$1"; }
in_needs() { printf '%s\n' "$needs" | grep -qxF "$1"; }

status=0
while read -r job kind; do
  [[ -z "$job" || "$job" == "$GATE" || "$kind" != "unconditional" ]] && continue
  if ! in_needs "$job" && ! allowlisted "$job"; then
    echo "check-merge-gate-needs: '$job' runs on every PR but is not in $GATE's needs:" >&2
    echo "  add it to $GATE's needs: in $WORKFLOW, or to ALLOWLIST in $0 with the reason." >&2
    status=1
  fi
done <<< "$jobs"

while read -r job reason; do
  [[ -z "$job" ]] && continue
  if [[ -z "$reason" ]]; then
    echo "check-merge-gate-needs: ALLOWLIST entry '$job' has no reason" >&2
    status=1
  fi
  kind="$(printf '%s\n' "$jobs" | awk -v j="$job" '$1 == j { print $2 }')"
  if [[ "$kind" != "unconditional" ]] || in_needs "$job"; then
    echo "check-merge-gate-needs: stale ALLOWLIST entry '$job' (job ${kind:-missing}, or already in needs); remove it" >&2
    status=1
  fi
done <<< "$ALLOWLIST"

if [[ $status -eq 0 ]]; then
  echo "check-merge-gate-needs: OK"
fi
exit $status
