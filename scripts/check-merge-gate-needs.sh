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
# The workflow is parsed as YAML (`yaml.safe_load`), not by indentation, so
# a job key with a trailing comment or quotes reads like any other (#1950).
# A job-level `if:` of exactly `always()` or `!cancelled()` (bare or inside
# `${{ }}`) still runs on every PR, so it counts as unconditional; anything
# else, `always() && <filter>` included, is conditional. A workflow it
# cannot read (invalid YAML, no jobs, no merge-gate, an empty needs) exits
# 2 rather than passing.
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
# Needs a python3 that can import PyYAML; ubuntu-latest's system python3
# can. Elsewhere, point MERGE_GATE_PYTHON at one that can, e.g.
#   uv venv "$TMPDIR/mgn" && uv pip install -p "$TMPDIR/mgn" pyyaml
#   MERGE_GATE_PYTHON="$TMPDIR/mgn/bin/python" bash scripts/check-merge-gate-needs.sh
#
# Self-test:
#   bash scripts/check-merge-gate-needs_test.sh

set -euo pipefail

WORKFLOW="${CI_WORKFLOW:-.github/workflows/ci.yml}"
GATE="merge-gate"
PYTHON="${MERGE_GATE_PYTHON:-python3}"

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

# "job <name> <conditional|unconditional>" per job, then "need <name>" per
# entry in merge-gate's needs. On anything unreadable it prints a message
# and exits non-zero, which this script turns into exit 2.
parse_workflow() {
  "$PYTHON" - "$@" <<'PY'
import re
import sys

try:
    import yaml
except ImportError:
    sys.exit("check-merge-gate-needs: PyYAML is not importable; set MERGE_GATE_PYTHON")

path, gate = sys.argv[1], sys.argv[2]
try:
    with open(path) as f:
        doc = yaml.safe_load(f)
except yaml.YAMLError as e:
    sys.exit(f"check-merge-gate-needs: {path} is not valid YAML: {e}")

jobs = doc.get("jobs") if isinstance(doc, dict) else None
if not isinstance(jobs, dict) or not jobs:
    sys.exit(f"check-merge-gate-needs: no jobs found under 'jobs:' in {path}")
if not isinstance(jobs.get(gate), dict):
    sys.exit(f"check-merge-gate-needs: no '{gate}' job in {path}")

ALWAYS = {"always()", "!cancelled()"}


def unconditional(job):
    if "if" not in job:
        return True
    cond = job["if"]
    if cond is True:
        return True
    if not isinstance(cond, str):
        return False
    cond = cond.strip()
    m = re.fullmatch(r"\$\{\{(.*)\}\}", cond, re.S)
    if m:
        cond = m.group(1)
    return re.sub(r"\s+", "", cond) in ALWAYS


for name, job in jobs.items():
    if not isinstance(job, dict):
        sys.exit(f"check-merge-gate-needs: job '{name}' is not a mapping in {path}")
    print("job", name, "unconditional" if unconditional(job) else "conditional")

needs = jobs[gate].get("needs")
if isinstance(needs, str):
    needs = [needs]
if not isinstance(needs, list) or not needs:
    sys.exit(f"check-merge-gate-needs: '{gate}' has no 'needs:' in {path}")
for n in needs:
    print("need", n)
PY
}
parsed="$(parse_workflow "$WORKFLOW" "$GATE")" || exit 2

# One line per job: "<job> <conditional|unconditional>".
jobs="$(printf '%s\n' "$parsed" | awk '$1 == "job" { print $2, $3 }')"
# merge-gate's needs, one job per line.
needs="$(printf '%s\n' "$parsed" | awk '$1 == "need" { print $2 }')"

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
