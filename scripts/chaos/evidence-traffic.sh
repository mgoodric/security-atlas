#!/usr/bin/env bash
#
# evidence-traffic.sh — synthetic mixed read/write traffic generator for the
# slice 335 Experiment 1 steady-state and injection windows (slice 354).
#
# Drives the two evidence surfaces the experiment's hypothesis names:
#
#   read   GET  /v1/evidence?control_id=...&limit=20
#   write  POST /v1/evidence:push          (one access_review.completion.v1 record)
#
# Emits one CSV row per request so P95 / error-rate can be computed per window
# by scripts/chaos/summarize-latency.sh:
#
#   epoch_s,op,http_code,latency_ms
#
# Latency is curl's own `%{time_total}`, so no shell or fork overhead is
# folded into the measurement.
#
# SCOPE BOUNDARY (slice 335 P0-335-2 / slice 354 P0-1) — LOAD-BEARING.
# The base URL is refused unless its host is a loopback literal
# (127.0.0.1 / localhost / ::1). This generator cannot be pointed at
# atlas-edge, a hosted tenant, or any other host on the network.
#
# Auth: the bearer JWT is read from the ATLAS_CHAOS_TOKEN environment
# variable ONLY — never from argv, which is world-readable via `ps`.
#
# Pacing: each wall-clock second the generator fires `--rate` requests and
# then sleeps to the next second boundary. Requests are therefore mildly
# bursty WITHIN a second but exact across seconds, which is what a 10-minute
# steady-state window needs. If the backend stalls, in-flight work is capped
# at MAX_INFLIGHT and the generator reports a shortfall at exit rather than
# forking without bound.
#
# Environment:
#   ATLAS_CHAOS_TOKEN     bearer JWT (required)
#   ATLAS_CHAOS_CONTROL   control_id used by reads and writes (required)
#
# Usage:
#   ATLAS_CHAOS_TOKEN=... ATLAS_CHAOS_CONTROL=... \
#     scripts/chaos/evidence-traffic.sh --duration-seconds 600 --out run.csv
#
# Exit: 0 completed, 1 refused (guard tripped), 2 env error.

set -eu

BASE_URL="http://127.0.0.1:8080"
RATE_PER_SEC=10
DURATION_SECONDS=60
OUT=""
LABEL="run"
MAX_INFLIGHT=64
REQ_TIMEOUT=10

die() {
  echo "evidence-traffic: $1" >&2
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
  --duration-seconds)
    DURATION_SECONDS="${2:-}"
    shift 2
    ;;
  --out)
    OUT="${2:-}"
    shift 2
    ;;
  --label)
    LABEL="${2:-}"
    shift 2
    ;;
  -h | --help)
    sed -n '2,42p' "$0"
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

if ! is_uint "$RATE_PER_SEC" || [ "$RATE_PER_SEC" -le 0 ]; then
  die "--rate must be a positive integer"
fi
if ! is_uint "$DURATION_SECONDS" || [ "$DURATION_SECONDS" -le 0 ]; then
  die "--duration-seconds must be a positive integer"
fi
if [ -z "$OUT" ]; then
  die "--out is required"
fi
case "$LABEL" in
*[!A-Za-z0-9_.-]*) die "--label has characters outside [A-Za-z0-9_.-]" ;;
esac

# ---------------------------------------------------------------------------
# Guard — loopback-only target. Anything else is refused outright.
# ---------------------------------------------------------------------------
case "$BASE_URL" in
"http://127.0.0.1" | "http://localhost") : ;;
"http://127.0.0.1:"* | "http://localhost:"*) : ;;
"http://[::1]" | "http://[::1]:"*) : ;;
*) die "base URL '$BASE_URL' is not loopback; this experiment is local-compose ONLY" 1 ;;
esac
# A path or credentials component would let the loopback prefix be spoofed
# (e.g. http://localhost:8080@edge.example.com/...). Refuse both.
case "${BASE_URL#http://}" in
*[/@]*) die "base URL must be scheme://host[:port] with no path or userinfo" 1 ;;
esac

if [ -z "${ATLAS_CHAOS_TOKEN:-}" ]; then
  die "ATLAS_CHAOS_TOKEN is unset"
fi
if [ -z "${ATLAS_CHAOS_CONTROL:-}" ]; then
  die "ATLAS_CHAOS_CONTROL is unset"
fi
case "$ATLAS_CHAOS_CONTROL" in
*[!A-Za-z0-9-]*) die "ATLAS_CHAOS_CONTROL is not a bare UUID" ;;
esac

command -v curl >/dev/null 2>&1 || die "curl not found on PATH"

[ -f "$OUT" ] || echo "epoch_s,op,http_code,latency_ms" >"$OUT"

# Each worker appends one short line (< 512 bytes) with >>, which is atomic on
# this platform, so concurrent workers cannot interleave rows.
fire_read() {
  _ts="$(date -u +%s)"
  _r="$(curl -s -o /dev/null -w '%{http_code} %{time_total}' --max-time "$REQ_TIMEOUT" \
    -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" \
    "$BASE_URL/v1/evidence?control_id=$ATLAS_CHAOS_CONTROL&limit=20" 2>/dev/null || echo "000 $REQ_TIMEOUT")"
  echo "$_r" | awk -v ts="$_ts" '{ printf "%s,read,%s,%d\n", ts, $1, ($2 * 1000) + 0.5 }' >>"$OUT"
}

fire_write() {
  _key="$1"
  _body="{\"records\":[{\"idempotency_key\":\"$_key\",\"evidence_kind\":\"access_review.completion.v1\",\"schema_version\":\"1.0.0\",\"control_id\":\"$ATLAS_CHAOS_CONTROL\",\"scope\":[{\"key\":\"env\",\"values\":[\"prod\"]}],\"observed_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"result\":\"pass\",\"payload\":{\"review_id\":\"$_key\",\"completed_by\":\"chaos-354\",\"reviewer_role\":\"security\",\"users_reviewed\":7},\"source_attribution\":{\"actor_type\":\"service\",\"actor_id\":\"chaos-354\"}}]}"
  _ts="$(date -u +%s)"
  _r="$(curl -s -o /dev/null -w '%{http_code} %{time_total}' --max-time "$REQ_TIMEOUT" \
    -X POST -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" -H 'Content-Type: application/json' \
    -d "$_body" "$BASE_URL/v1/evidence:push" 2>/dev/null || echo "000 $REQ_TIMEOUT")"
  echo "$_r" | awk -v ts="$_ts" '{ printf "%s,write,%s,%d\n", ts, $1, ($2 * 1000) + 0.5 }' >>"$OUT"
}

inflight() { jobs -pr | wc -l | tr -d ' '; }

RUN_ID="$(date -u +%s)"
echo "evidence-traffic: label=$LABEL rate=${RATE_PER_SEC}/s duration=${DURATION_SECONDS}s out=$OUT"
echo "evidence-traffic: label=$LABEL start $(date -u +%Y-%m-%dT%H:%M:%SZ) epoch_s=$RUN_ID"

SKIPPED=0
sec=0
while [ "$sec" -lt "$DURATION_SECONDS" ]; do
  _boundary="$(date -u +%s)"
  i=0
  while [ "$i" -lt "$RATE_PER_SEC" ]; do
    if [ "$(inflight)" -ge "$MAX_INFLIGHT" ]; then
      # Backend is stalling. Record the shortfall rather than piling on;
      # the missing requests are reported at exit so the run's offered load
      # is never overstated.
      SKIPPED=$((SKIPPED + 1))
    else
      # Alternate read/write 50:50. Slice 335 says "mixed read/write" without
      # pinning a ratio; an even split gives both surfaces an equal sample.
      if [ $((i % 2)) -eq 0 ]; then
        fire_read &
      else
        fire_write "chaos354-$LABEL-$RUN_ID-$sec-$i" &
      fi
    fi
    i=$((i + 1))
  done

  # Sleep to the next wall-clock second boundary.
  while [ "$(date -u +%s)" -le "$_boundary" ]; do
    sleep 0.05
  done
  sec=$((sec + 1))
done

wait
echo "evidence-traffic: label=$LABEL done $(date -u +%Y-%m-%dT%H:%M:%SZ) skipped=$SKIPPED"
