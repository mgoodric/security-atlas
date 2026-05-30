#!/usr/bin/env bash
#
# measure-integration-wallclock.sh — slice 353 (Q-8 from slice 333's QA audit)
#
# Record the Go integration job's wall-clock to a watermark file and
# warn when it crosses the Phase-A/B-split trigger (default 20 min).
#
# Background (slice 333 finding Q-8): the integration job runs serially
# (`-p 1`, slice 334's load-bearing rationale). Its wall-clock grows as
# the package list grows. There is no plan for what happens when the
# wall-clock crosses the developer-patience ceiling (~20-30 min). This
# script converts an undefined future problem into a tracked watermark:
# every clean main run appends a measurement; when the measured duration
# exceeds the trigger, the script prints the action ("file the Phase A/B
# split slice").
#
# This is a RECORDER, not a test. It does not assert on a single wall-
# clock sample (per the slice-381 lesson — single-sample timing
# assertions flake). The 20-min trigger is a generous ceiling far above
# the current ~10-15 min baseline; crossing it is a durable signal, not
# a per-run flake. The watermark file accumulates a history so the trend
# is visible over time.
#
# Two modes:
#   (1) MEASURE mode (default): run the integration test command, time
#       it, append the result. Used in CI on clean main runs.
#   (2) RECORD mode (WALLCLOCK_SECONDS set): skip running anything,
#       record the provided duration. Used by CI when the job's own
#       timing is already known, and by the self-test for determinism.
#
# Inputs (env):
#   WALLCLOCK_FILE      Watermark path (default: docs/integration-wallclock.tsv)
#   WALLCLOCK_TRIGGER   Trigger seconds (default: 1200 = 20 min)
#   WALLCLOCK_SECONDS   If set, RECORD this duration instead of measuring
#   WALLCLOCK_CMD       Command to time in MEASURE mode
#                       (default: go test -tags=integration -p 1 ./internal/...)
#   WALLCLOCK_SHA       Commit SHA to stamp (default: `git rev-parse --short HEAD`)
#   WALLCLOCK_DRY_RUN   "true" to skip the file append (default: false)
#
# Watermark format (TSV, append-only):
#   <iso8601_utc>\t<short_sha>\t<seconds>\t<status>
#   status ∈ {ok, OVER_TRIGGER}
#
# Exit codes:
#   0 — recorded successfully (OVER_TRIGGER is a warning, NOT a failure —
#       crossing the trigger is a signal to file a slice, not a CI block)
#   1 — MEASURE mode and the timed command itself failed
#   2 — environment misconfigured
#
# Usage:
#   bash scripts/measure-integration-wallclock.sh                # measure
#   WALLCLOCK_SECONDS=930 bash scripts/measure-integration-wallclock.sh   # record
#
# Self-test:
#   bash scripts/measure-integration-wallclock_test.sh

set -euo pipefail

WALLCLOCK_FILE="${WALLCLOCK_FILE:-docs/integration-wallclock.tsv}"
TRIGGER="${WALLCLOCK_TRIGGER:-1200}"
CMD="${WALLCLOCK_CMD:-go test -tags=integration -p 1 ./internal/...}"
DRY_RUN="${WALLCLOCK_DRY_RUN:-false}"

if ! [[ "$TRIGGER" =~ ^[0-9]+$ ]]; then
  echo "measure-integration-wallclock: WALLCLOCK_TRIGGER must be an integer (got '$TRIGGER')" >&2
  exit 2
fi

sha="${WALLCLOCK_SHA:-}"
if [[ -z "$sha" ]]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --short HEAD >/dev/null 2>&1; then
    sha="$(git rev-parse --short HEAD)"
  else
    sha="unknown"
  fi
fi

cmd_rc=0
if [[ -n "${WALLCLOCK_SECONDS:-}" ]]; then
  # RECORD mode.
  if ! [[ "$WALLCLOCK_SECONDS" =~ ^[0-9]+$ ]]; then
    echo "measure-integration-wallclock: WALLCLOCK_SECONDS must be an integer (got '$WALLCLOCK_SECONDS')" >&2
    exit 2
  fi
  seconds="$WALLCLOCK_SECONDS"
else
  # MEASURE mode — time the command.
  start="$(date +%s)"
  set +e
  bash -c "$CMD"
  cmd_rc=$?
  set -e
  end="$(date +%s)"
  seconds=$(( end - start ))
fi

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

status="ok"
if (( seconds > TRIGGER )); then
  status="OVER_TRIGGER"
fi

if [[ "$DRY_RUN" != "true" ]]; then
  dir="$(dirname "$WALLCLOCK_FILE")"
  mkdir -p "$dir"
  if [[ ! -f "$WALLCLOCK_FILE" ]]; then
    printf '# Integration-job wall-clock watermark (slice 353 / Q-8). Append-only.\n' > "$WALLCLOCK_FILE"
    printf '# Columns: timestamp_utc\tshort_sha\tseconds\tstatus\n' >> "$WALLCLOCK_FILE"
    printf '# Trigger: when seconds > %s the integration job crosses the Phase-A/B-split watermark — file the split slice.\n' "$TRIGGER" >> "$WALLCLOCK_FILE"
  fi
  printf '%s\t%s\t%s\t%s\n' "$ts" "$sha" "$seconds" "$status" >> "$WALLCLOCK_FILE"
fi

mins=$(( seconds / 60 ))
echo "measure-integration-wallclock: recorded ${seconds}s (~${mins} min), status=${status}, sha=${sha}"
if [[ "$status" == "OVER_TRIGGER" ]]; then
  echo "" >&2
  echo "measure-integration-wallclock: TRIGGER CROSSED — integration wall-clock ${seconds}s > ${TRIGGER}s." >&2
  echo "  ACTION: file the Phase-A/B integration-split slice (serial Phase A + parallel" >&2
  echo "  Phase B), per slice 334's future-relaxation path. Slice 333 Q-8 / slice 353." >&2
fi

if (( cmd_rc != 0 )); then
  echo "measure-integration-wallclock: the timed command exited non-zero ($cmd_rc)" >&2
  exit 1
fi
exit 0
