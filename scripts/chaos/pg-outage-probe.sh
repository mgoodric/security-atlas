#!/usr/bin/env bash
#
# pg-outage-probe.sh — synthetic request generator for slice 335
# Experiment 3 ("Postgres primary unavailable"), executed by slice 356a.
#
# Drives exactly the three surfaces the design's Method names:
#
#   read    GET  /v1/anchors            (bearer-required read path)
#   write   POST /v1/evidence:push      (bearer-required write path)
#   health  GET  /health                (unauthenticated liveness probe)
#
# and, additionally, the literal path the design's Method text spells:
#
#   healthz GET  /healthz               (see NAMING note below)
#
# NAMING (recorded, not silently reconciled). The slice 335 design names
# the health endpoint `/healthz`. The shipped router registers `/health`
# (internal/api/register_platform.go). `/healthz` is not a registered
# route, so it falls through to the auth middleware and answers 401 even
# in steady state. This probe fires BOTH so the results log can report the
# design-vs-implementation divergence with evidence rather than quietly
# substituting one for the other.
#
# OUTPUT — one TSV row per request, appended atomically:
#
#   epoch_s  phase  op  http_code  latency_ms  body
#
# `body` is the response body with tabs/newlines collapsed to spaces and
# truncated to $BODY_MAX chars. Experiment 3's falsification check is about
# response BODY SHAPE (no stack traces), so unlike a pure latency harness
# this probe must retain bodies, not discard them.
#
# An `http_code` of 000 means curl produced no response — either a hard
# connection error or the --max-time ceiling was hit. The recorded
# latency_ms distinguishes the two.
#
# LATENCY SOURCE — LOAD-BEARING, and a real trap. curl's `%{time_total}`
# is authoritative for a request that COMPLETED, but curl reports
# `0.000000` for it when the transfer fails (verified on this platform:
# both a connection refusal, exit 7, and a `--max-time` timeout, exit 28,
# write `000 0.000000`). Trusting `time_total` alone would therefore
# record a 35-second hang as 0ms and make the design's "no request hangs
# > 30s" check silently unfalsifiable — the check would pass by
# construction no matter how badly the platform hung.
#
# So: when curl returns http_code 000, this probe substitutes the
# SHELL-MEASURED wall time across the curl invocation. Resolution is one
# second (bash 3.2 on this platform has no EPOCHREALTIME), which is ample
# for a 30-second threshold. Completed requests keep curl's sub-ms
# `time_total`, so steady-state latency is not coarsened.
#
# TIMEOUT CEILING — LOAD-BEARING. --max-time defaults to 35s, deliberately
# ABOVE the design's 30s abort threshold ("any request hangs > 30 seconds
# with no response"). A ceiling at or below 30s would make the falsification
# check unobservable: every hang would be truncated to the ceiling and no
# run could ever demonstrate a >30s hang. Do not lower this below 31.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 356
# P0-1) — LOAD-BEARING. The base URL is refused unless its host is a
# loopback literal (127.0.0.1 / localhost / ::1). This probe cannot be
# aimed at atlas-edge, a hosted tenant, or any other host on the network.
#
# Auth: the bearer JWT is read from ATLAS_CHAOS_TOKEN only — never from
# argv, which is world-readable via `ps` on this platform.
#
# Environment:
#   ATLAS_CHAOS_TOKEN     bearer JWT (required)
#   ATLAS_CHAOS_CONTROL   control_id used by the write probe (required)
#
# Usage:
#   ATLAS_CHAOS_TOKEN=... ATLAS_CHAOS_CONTROL=... \
#     scripts/chaos/pg-outage-probe.sh --phase injection \
#       --duration-seconds 60 --out run.tsv
#
# Exit: 0 completed, 1 refused (scope guard tripped), 2 environment error.

set -Eeuo pipefail

BASE_URL="http://127.0.0.1:58080"
DURATION_SECONDS=60
PHASE="run"
OUT=""
REQ_TIMEOUT=35
BODY_MAX=512
MAX_INFLIGHT=32

die() {
  echo "pg-outage-probe: $1" >&2
  exit "${2:-2}"
}

is_uint() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

while [[ $# -gt 0 ]]; do
  case "$1" in
  --base-url)
    BASE_URL="${2:-}"
    shift 2
    ;;
  --duration-seconds)
    DURATION_SECONDS="${2:-}"
    shift 2
    ;;
  --phase)
    PHASE="${2:-}"
    shift 2
    ;;
  --out)
    OUT="${2:-}"
    shift 2
    ;;
  --timeout)
    REQ_TIMEOUT="${2:-}"
    shift 2
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

# --- scope guard ------------------------------------------------------
# Parse the host out of the base URL and refuse anything non-loopback.
# This runs BEFORE any other validation so a mis-aimed run dies on the
# boundary, not on a missing token.
_hostport="${BASE_URL#*://}"
_hostport="${_hostport%%/*}"
_host="${_hostport%%:*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "pg-outage-probe: REFUSED — base URL host '$_host' is not loopback." >&2
  echo "  Slice 335 scope discipline restricts every experiment to the" >&2
  echo "  local docker-compose stack. This probe will not target" >&2
  echo "  atlas-edge, a hosted tenant, or any other host." >&2
  exit 1
  ;;
esac

is_uint "$DURATION_SECONDS" || die "--duration-seconds must be a non-negative integer"
is_uint "$REQ_TIMEOUT" || die "--timeout must be a non-negative integer"
[[ "$REQ_TIMEOUT" -gt 30 ]] || die "--timeout must exceed the design's 30s abort threshold (got ${REQ_TIMEOUT})"
[[ -n "$OUT" ]] || die "--out is required"
[[ -n "${ATLAS_CHAOS_TOKEN:-}" ]] || die "ATLAS_CHAOS_TOKEN is unset"
[[ -n "${ATLAS_CHAOS_CONTROL:-}" ]] || die "ATLAS_CHAOS_CONTROL is unset"
[[ "$ATLAS_CHAOS_CONTROL" =~ ^[A-Za-z0-9-]+$ ]] || die "ATLAS_CHAOS_CONTROL is not a bare UUID"
[[ "$PHASE" =~ ^[a-z0-9_-]+$ ]] || die "--phase must be [a-z0-9_-]+"
command -v curl >/dev/null 2>&1 || die "curl not found on PATH"

[[ -f "$OUT" ]] || printf 'epoch_s\tphase\top\thttp_code\tlatency_ms\tbody\n' >"$OUT"

# Normalize a response body into a single TSV-safe field: collapse
# tabs/CR/LF to spaces, then truncate. Truncation is marked so a reader
# never mistakes a clipped body for a complete one.
normalize_body() {
  local raw="$1" flat
  flat="$(printf '%s' "$raw" | tr '\t\r\n' '   ')"
  if [[ ${#flat} -gt $BODY_MAX ]]; then
    printf '%s...[truncated]' "${flat:0:$BODY_MAX}"
  else
    printf '%s' "$flat"
  fi
}

# Each probe writes one line with >> . Lines stay well under the 512-byte
# PIPE_BUF-style atomicity window on this platform once the body is capped,
# so concurrent probes cannot interleave rows.
emit() {
  local ts="$1" op="$2" meta="$3" raw="$4" wall_s="$5"
  local code lat_s lat_ms
  code="${meta%% *}"
  lat_s="${meta##* }"
  lat_ms="$(awk -v s="$lat_s" 'BEGIN { printf "%d", (s * 1000) + 0.5 }')"
  # See LATENCY SOURCE in the header: curl zeroes time_total on a failed
  # transfer, so a 000 uses the shell's wall clock instead. Without this
  # substitution every hang would be recorded as 0ms.
  if [[ "$code" == "000" ]]; then
    lat_ms=$((wall_s * 1000))
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$ts" "$PHASE" "$op" "$code" "$lat_ms" "$(normalize_body "$raw")" >>"$OUT"
}

# curl writes the body to stdout and appends a sentinel line carrying the
# status code and total time, so one invocation yields both without a
# second request.
SENTINEL='@@PROBE@@'

run_probe() {
  local op="$1"
  shift
  local ts end out raw meta wall
  ts="$(date -u +%s)"
  out="$(curl -s --max-time "$REQ_TIMEOUT" \
    -w "\n${SENTINEL}%{http_code} %{time_total}" "$@" 2>/dev/null || true)"
  end="$(date -u +%s)"
  wall=$((end - ts))
  if [[ "$out" == *"$SENTINEL"* ]]; then
    meta="${out##*$SENTINEL}"
    raw="${out%$SENTINEL*}"
  else
    # Sentinel absent entirely — curl died before writing it. Fall back
    # to the wall clock for the same reason emit() does.
    meta="000 0"
    raw="$out"
  fi
  emit "$ts" "$op" "$meta" "$raw" "$wall"
}

probe_read() {
  run_probe read -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" \
    "$BASE_URL/v1/anchors"
}

probe_health() {
  run_probe health "$BASE_URL/health"
}

# The design's literal path. Fired for the record; see NAMING above.
probe_healthz() {
  run_probe healthz "$BASE_URL/healthz"
}

probe_write() {
  local key="$1" body
  body="$(
    cat <<JSON
{"records":[{"idempotency_key":"$key","evidence_kind":"access_review.completion.v1","schema_version":"1.0.0","control_id":"$ATLAS_CHAOS_CONTROL","scope":[{"key":"env","values":["prod"]}],"observed_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","result":"pass","payload":{"review_id":"$key","completed_by":"chaos-356a","reviewer_role":"security","users_reviewed":7},"source_attribution":{"actor_type":"service","actor_id":"chaos-356a"}}]}
JSON
  )"
  run_probe write -X POST \
    -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" \
    -H 'Content-Type: application/json' \
    -d "$body" "$BASE_URL/v1/evidence:push"
}

inflight() { jobs -pr | wc -l | tr -d ' '; }

RUN_ID="$(date -u +%s)"
echo "pg-outage-probe: phase=$PHASE duration=${DURATION_SECONDS}s timeout=${REQ_TIMEOUT}s out=$OUT"
echo "pg-outage-probe: start $(date -u +%Y-%m-%dT%H:%M:%SZ) epoch_s=$RUN_ID"

# One round per wall-clock second: all four probes fired CONCURRENTLY so a
# stalled read cannot delay the health probe behind it (serial rounds would
# conflate "atlas is slow" with "the probe queued"). The round then sleeps
# to the next second boundary; a round that overruns its second (because
# every probe is timing out) simply starts the next one late, and the
# recorded epoch_s keeps the timeline honest.
SKIPPED=0
sec=0
while [[ "$sec" -lt "$DURATION_SECONDS" ]]; do
  boundary="$(date -u +%s)"
  if [[ "$(inflight)" -ge "$MAX_INFLIGHT" ]]; then
    # Every prior round is still hung. Record the shortfall rather than
    # forking without bound; the count is reported at exit so the run's
    # offered load is never overstated.
    SKIPPED=$((SKIPPED + 1))
  else
    probe_read &
    probe_write "chaos356a-$PHASE-$RUN_ID-$sec" &
    probe_health &
    probe_healthz &
  fi
  sec=$((sec + 1))
  now="$(date -u +%s)"
  if [[ "$now" -lt $((boundary + 1)) ]]; then
    sleep "$((boundary + 1 - now))"
  fi
done

# Wait out stragglers. A request still in flight here is exactly the >30s
# hang the falsification check is looking for, so it must be allowed to
# finish and be recorded — never killed.
wait

echo "pg-outage-probe: phase=$PHASE done $(date -u +%Y-%m-%dT%H:%M:%SZ) rounds_skipped=$SKIPPED"
exit 0
