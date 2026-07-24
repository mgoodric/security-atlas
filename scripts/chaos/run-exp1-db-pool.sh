#!/usr/bin/env bash
#
# run-exp1-db-pool.sh — orchestrator for slice 335 Experiment 1
# (evidence-ledger DB connection-pool exhaustion), executed by slice 354.
#
# Runs the design's Method verbatim against the LOCAL docker-compose stack:
#
#   1. synthetic mixed read/write traffic at the baseline rate, continuously
#   2. steady-state window   (default 600s) — captured BEFORE any injection
#   3. injection window      (default 300s) — scripts/chaos/db-pool-hog.sh
#   4. recovery window       (default 300s) — storm released, traffic continues
#
# Alongside the traffic it samples pg_stat_activity and the evidence_records
# row count every 5s, so pool occupancy and ledger growth are recorded across
# all three windows.
#
# ABORT CRITERIA (slice 335 Experiment 1, enforced by the watchdog below):
#   * P95 evidence-read > 5000ms sustained over a trailing 60s window, OR
#   * synthetic error rate > 50% sustained over a trailing 60s window, OR
#   * the postgres container leaves the running state (OOM / supervisor kill).
# Tripping any of them releases the storm immediately and marks the run
# ABORTED in the artifact directory. The run is NOT retried automatically.
#
# SCOPE BOUNDARY: every network target is delegated to the two scripts this
# calls, both of which refuse non-local targets by construction. This
# orchestrator adds no endpoint of its own.
#
# Environment:
#   ATLAS_CHAOS_TOKEN     bearer JWT (required; never passed via argv)
#   ATLAS_CHAOS_CONTROL   control_id driven by reads and writes (required)
#
# Usage:
#   ATLAS_CHAOS_TOKEN=... ATLAS_CHAOS_CONTROL=... \
#     scripts/chaos/run-exp1-db-pool.sh --base-url http://127.0.0.1:8080 \
#       --artifacts /tmp/exp1
#
# Exit: 0 completed, 3 aborted by watchdog, 1 refused, 2 env error.

set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

BASE_URL="http://127.0.0.1:8080"
RATE_PER_SEC=10
STEADY_SECONDS=600
INJECT_SECONDS=300
RECOVER_SECONDS=300
ARTIFACTS=""
PG_CONTAINER="${ATLAS_CHAOS_PG_CONTAINER:-security-atlas-postgres-1}"

ABORT_P95_MS=5000
ABORT_ERROR_PCT=50
ABORT_WINDOW_S=60

die() {
  echo "run-exp1: $1" >&2
  exit "${2:-2}"
}

is_uint() {
  case "$1" in
  '' | *[!0-9]*) return 1 ;;
  *) return 0 ;;
  esac
}

while [ $# -gt 0 ]; do
  case "$1" in
  --base-url)
    BASE_URL="${2:-}"
    shift 2
    ;;
  --rate)
    RATE_PER_SEC="${2:-}"
    shift 2
    ;;
  --steady-seconds)
    STEADY_SECONDS="${2:-}"
    shift 2
    ;;
  --inject-seconds)
    INJECT_SECONDS="${2:-}"
    shift 2
    ;;
  --recover-seconds)
    RECOVER_SECONDS="${2:-}"
    shift 2
    ;;
  --artifacts)
    ARTIFACTS="${2:-}"
    shift 2
    ;;
  -h | --help)
    sed -n '2,42p' "$0"
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[ -n "$ARTIFACTS" ] || die "--artifacts <dir> is required"
for v in "$RATE_PER_SEC" "$STEADY_SECONDS" "$INJECT_SECONDS" "$RECOVER_SECONDS"; do
  is_uint "$v" || die "window/rate arguments must be non-negative integers"
done
[ -n "${ATLAS_CHAOS_TOKEN:-}" ] || die "ATLAS_CHAOS_TOKEN is unset"
[ -n "${ATLAS_CHAOS_CONTROL:-}" ] || die "ATLAS_CHAOS_CONTROL is unset"

mkdir -p "$ARTIFACTS"
CSV="$ARTIFACTS/traffic.csv"
DBLOG="$ARTIFACTS/db-samples.csv"
TIMELINE="$ARTIFACTS/timeline.txt"
ABORT_FLAG="$ARTIFACTS/ABORTED"
rm -f "$ABORT_FLAG"

TOTAL=$((STEADY_SECONDS + INJECT_SECONDS + RECOVER_SECONDS))

note() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*" | tee -a "$TIMELINE"; }

# ---------------------------------------------------------------------------
# DB sampler — pool occupancy + ledger growth every 5s for the whole run.
# ---------------------------------------------------------------------------
db_sampler() {
  echo "epoch_s,total_conns,atlas_app_conns,other_conns,evidence_rows" >"$DBLOG"
  _end=$(($(date -u +%s) + TOTAL + 15))
  while [ "$(date -u +%s)" -lt "$_end" ]; do
    _row="$(docker exec "$PG_CONTAINER" psql -U postgres -d security_atlas -Atc \
      "select (select count(*) from pg_stat_activity), (select count(*) from pg_stat_activity where usename='atlas_app'), (select count(*) from pg_stat_activity where usename is distinct from 'atlas_app'), (select count(*) from evidence_records);" \
      2>/dev/null | tr '|' ',' || echo 'ERR,ERR,ERR,ERR')"
    echo "$(date -u +%s),$_row" >>"$DBLOG"
    sleep 5
  done
}

# ---------------------------------------------------------------------------
# Watchdog — enforces the design's abort criteria on a trailing window.
# ---------------------------------------------------------------------------
watchdog() {
  _end=$(($(date -u +%s) + TOTAL))
  while [ "$(date -u +%s)" -lt "$_end" ]; do
    sleep 15
    [ -f "$CSV" ] || continue

    if [ "$(docker inspect --format '{{.State.Running}}' "$PG_CONTAINER" 2>/dev/null || echo false)" != "true" ]; then
      note "ABORT: postgres container '$PG_CONTAINER' is not running (OOM / supervisor kill)"
      echo "postgres_not_running" >"$ABORT_FLAG"
      return
    fi

    _lo=$(($(date -u +%s) - ABORT_WINDOW_S))
    _verdict="$(awk -F, -v lo="$_lo" -v p95cap="$ABORT_P95_MS" -v errcap="$ABORT_ERROR_PCT" '
      NR == 1 { next }
      $1 + 0 < lo { next }
      { n++; if ($3 + 0 < 200 || $3 + 0 > 299) e++; if ($2 == "read") { rn++; rlat[rn] = $4 + 0 } }
      END {
        if (n < 10) { print "ok"; exit }
        if ((e * 100.0) / n > errcap) { print "error_rate"; exit }
        if (rn > 0) {
          for (i = 1; i <= rn; i++) for (j = i + 1; j <= rn; j++) if (rlat[j] < rlat[i]) { t = rlat[i]; rlat[i] = rlat[j]; rlat[j] = t }
          if (rlat[int((0.95 * rn) + 0.999999)] > p95cap) { print "read_p95"; exit }
        }
        print "ok"
      }
    ' "$CSV")"

    if [ "$_verdict" != "ok" ]; then
      note "ABORT: criterion tripped ($_verdict) over trailing ${ABORT_WINDOW_S}s"
      echo "$_verdict" >"$ABORT_FLAG"
      return
    fi
  done
}

release_storm() {
  pkill -f 'db-pool-hog.sh' >/dev/null 2>&1 || true
  docker exec "$PG_CONTAINER" pkill -f 'SELECT pg_sleep' >/dev/null 2>&1 || true
}

cleanup() {
  release_storm
  kill "${TRAFFIC_PID:-}" "${SAMPLER_PID:-}" "${WATCHDOG_PID:-}" >/dev/null 2>&1 || true
}
trap 'cleanup; exit 130' INT TERM

: >"$TIMELINE"
note "RUN START base_url=$BASE_URL rate=${RATE_PER_SEC}/s steady=${STEADY_SECONDS}s inject=${INJECT_SECONDS}s recover=${RECOVER_SECONDS}s"
note "artifacts=$ARTIFACTS"

RUN_T0="$(date -u +%s)"
note "T0 epoch_s=$RUN_T0"

db_sampler &
SAMPLER_PID=$!
watchdog &
WATCHDOG_PID=$!

bash "$ROOT/scripts/chaos/evidence-traffic.sh" \
  --base-url "$BASE_URL" --rate "$RATE_PER_SEC" \
  --duration-seconds "$TOTAL" --out "$CSV" --label exp1 >"$ARTIFACTS/traffic.log" 2>&1 &
TRAFFIC_PID=$!

note "STEADY_START epoch_s=$(date -u +%s)"
_wake=$((RUN_T0 + STEADY_SECONDS))
while [ "$(date -u +%s)" -lt "$_wake" ] && [ ! -f "$ABORT_FLAG" ]; do sleep 2; done
note "STEADY_END epoch_s=$(date -u +%s)"

if [ -f "$ABORT_FLAG" ]; then
  note "aborted before injection; releasing and stopping"
  cleanup
  exit 3
fi

note "INJECT_START epoch_s=$(date -u +%s)"
bash "$ROOT/scripts/chaos/db-pool-hog.sh" --hold-seconds "$INJECT_SECONDS" \
  >"$ARTIFACTS/db-pool-hog.log" 2>&1 &
HOG_PID=$!

_wake=$((RUN_T0 + STEADY_SECONDS + INJECT_SECONDS))
while [ "$(date -u +%s)" -lt "$_wake" ] && [ ! -f "$ABORT_FLAG" ]; do sleep 2; done

note "ROLLBACK epoch_s=$(date -u +%s) (releasing connection storm)"
release_storm
wait "$HOG_PID" 2>/dev/null || true
note "INJECT_END epoch_s=$(date -u +%s)"

if [ -f "$ABORT_FLAG" ]; then
  note "aborted during injection; storm released"
  cleanup
  exit 3
fi

note "RECOVER_START epoch_s=$(date -u +%s)"
wait "$TRAFFIC_PID" 2>/dev/null || true
note "RECOVER_END epoch_s=$(date -u +%s)"

kill "$SAMPLER_PID" "$WATCHDOG_PID" >/dev/null 2>&1 || true
note "RUN END"
