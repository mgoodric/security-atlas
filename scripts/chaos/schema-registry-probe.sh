#!/usr/bin/env bash
#
# schema-registry-probe.sh — the synthetic push + registry-read client for
# slice 335 Experiment 7 ("schema-registry unavailable"), executed by slice
# 358.
#
# The design's Method is a sequence of pushes rather than a load profile: a
# known (hot-cached) kind, an unknown kind, and — for the hot-cache-vs-
# cold-miss distinction slice 358 exists to draw — a kind that IS registered
# but is NOT in the process's hot cache. This probe drives all three plus the
# registry's own read surface, one tick per second, and records what came
# back.
#
# FOUR PROBE CLASSES PER TICK. Each answers a different half of the design's
# hypothesis, and running them in the same tick means they observe the same
# instant of the injection window:
#
#   known           push of a global kind that was pushed once in the
#                   current atlas process lifetime, so the registry's
#                   in-process cache holds it. The design's "cached schemas
#                   continue to work" conjunct.
#
#   unknown         push of a kind registered nowhere. The design's
#                   fail-fast conjunct: this is the push that must NOT be
#                   silently accepted with an unvalidated payload.
#
#   cold            push of a TENANT-PRIVATE kind that is registered in the
#                   registry's Postgres store but has never been pushed, so
#                   it can only resolve through the registry's DB lookup
#                   path (`Service.lookupCompiled` slow path). This is the
#                   probe that separates "the registry is down" from "the
#                   kind does not exist" — a cold-miss kind is VALID, and
#                   what happens to it during the outage is the experiment's
#                   real question. Pushed exactly ONCE per phase (at
#                   --cold-at-tick) because the first push hydrates the
#                   tenant cache and the kind stops being cold.
#
#   registry_list   GET /v1/schemas       — the registry's own read surface.
#   registry_get    GET /v1/schemas/K/V   — same, single-kind path.
#                   Both are recorded because they take DIFFERENT error
#                   paths inside the service, and the design's claim is
#                   about the SHAPE of the error under failure.
#
# SURFACE CHOICE. The design states its expectations as HTTP status codes
# (2xx / 400 / 503), so the probe drives the REST push API
# (POST /v1/evidence:push) rather than the gRPC RPC. Auth is a JWT because
# slice 326 retired the legacy bearer path on /v1/*; the orchestrator mints
# one via the slice-201 env-gated /v1/test/issue-jwt endpoint.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 358
# P0-1) — LOAD-BEARING. The HTTP base is refused unless its host is a
# loopback literal. This probe cannot be aimed at atlas-edge, a hosted
# tenant, or any other host on the network.
#
# SECRETS. The JWT is read from a 0600 file named by --jwt-file and passed
# to curl through a header file, never on argv (argv is world-readable via
# `ps` on this platform).
#
# OUTPUT — one TSV row per REQUEST, appended atomically:
#
#   epoch_s  phase  tick  class  idem_key  http_code  err_code  wall_ms  record_id
#
# `err_code` is the `code` field of the platform's structured error body, or
# `-` when the response carried none (including every success). `record_id`
# is the receipt's record id on a 2xx push, else `-`. A receipt id here does
# NOT mean the record reached the ledger — the orchestrator resolves that
# separately against Postgres, and the gap between the two is the point.
#
# Usage:
#   scripts/chaos/schema-registry-probe.sh \
#     --http-base http://127.0.0.1:38080 --jwt-file ./jwt \
#     --control <uuid> --run-id exp7r1 --phase steady \
#     --duration-seconds 60 --known-kind access_review.completion.v1 \
#     --unknown-kind chaos358.absent.v1 --cold-kind chaos358.cold_steady.v1 \
#     --out steady.tsv
#
# Exit: 0 completed, 1 refused (scope guard tripped), 2 environment error.

set -Eeuo pipefail

HTTP_BASE="http://127.0.0.1:38080"
JWT_FILE=""
CONTROL_ID=""
RUN_ID=""
PHASE=""
DURATION_SECONDS=60
KNOWN_KIND="access_review.completion.v1"
KNOWN_SEMVER="1.0.0"
UNKNOWN_KIND=""
COLD_KIND=""
COLD_AT_TICK=5
OUT=""
CONNECT_TIMEOUT=3
MAX_TIME=10

die() {
  echo "schema-registry-probe: $1" >&2
  exit "${2:-2}"
}

is_uint() { [[ "$1" =~ ^[0-9]+$ ]]; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --http-base)
    HTTP_BASE="${2:-}"
    shift 2
    ;;
  --jwt-file)
    JWT_FILE="${2:-}"
    shift 2
    ;;
  --control)
    CONTROL_ID="${2:-}"
    shift 2
    ;;
  --run-id)
    RUN_ID="${2:-}"
    shift 2
    ;;
  --phase)
    PHASE="${2:-}"
    shift 2
    ;;
  --duration-seconds)
    DURATION_SECONDS="${2:-}"
    shift 2
    ;;
  --known-kind)
    KNOWN_KIND="${2:-}"
    shift 2
    ;;
  --known-semver)
    KNOWN_SEMVER="${2:-}"
    shift 2
    ;;
  --unknown-kind)
    UNKNOWN_KIND="${2:-}"
    shift 2
    ;;
  --cold-kind)
    COLD_KIND="${2:-}"
    shift 2
    ;;
  --cold-at-tick)
    COLD_AT_TICK="${2:-}"
    shift 2
    ;;
  --out)
    OUT="${2:-}"
    shift 2
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[[ -n "$OUT" ]] || die "--out is required"
[[ -n "$RUN_ID" ]] || die "--run-id is required"
[[ -n "$PHASE" ]] || die "--phase is required"
[[ -n "$CONTROL_ID" ]] || die "--control is required"
[[ -n "$JWT_FILE" ]] || die "--jwt-file is required"
[[ -f "$JWT_FILE" ]] || die "jwt file not found: $JWT_FILE"
[[ -n "$UNKNOWN_KIND" ]] || die "--unknown-kind is required"
[[ -n "$COLD_KIND" ]] || die "--cold-kind is required"
is_uint "$DURATION_SECONDS" || die "--duration-seconds must be a non-negative integer"
is_uint "$COLD_AT_TICK" || die "--cold-at-tick must be a non-negative integer"
[[ "$RUN_ID" =~ ^[A-Za-z0-9_-]+$ ]] || die "--run-id must be [A-Za-z0-9_-]+"
[[ "$PHASE" =~ ^[A-Za-z0-9_-]+$ ]] || die "--phase must be [A-Za-z0-9_-]+"

# --- scope guard: loopback HTTP base ------------------------------------
_hostport="${HTTP_BASE#*://}"
_host="${_hostport%%:*}"
_host="${_host%%/*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "schema-registry-probe: REFUSED — HTTP base host '$_host' is not loopback." >&2
  echo "  Slice 335 restricts every experiment to the local docker-compose" >&2
  echo "  stack (P0-335-2 / slice 358 P0-1). Refusing to proceed." >&2
  exit 1
  ;;
esac

JWT="$(cat "$JWT_FILE")"
[[ -n "$JWT" ]] || die "jwt file $JWT_FILE is empty"

# Header file rather than -H on argv: argv is world-readable via `ps`.
TMPDIR_PROBE="$(mktemp -d)"
cleanup() { rm -rf "$TMPDIR_PROBE"; }
trap cleanup EXIT
HDR_FILE="$TMPDIR_PROBE/hdr"
umask 077
{
  printf 'Authorization: Bearer %s\n' "$JWT"
  printf 'Content-Type: application/json\n'
} >"$HDR_FILE"

: >"$OUT"

# emit appends one already-formed TSV line. Single writes of this size are
# atomic under PIPE_BUF.
emit() { printf '%s\n' "$1" >>"$OUT"; }

# err_code_of extracts the platform's structured error `code` from a
# response body, or `-` when absent.
err_code_of() {
  local body_file="$1" code
  code="$(sed -n 's/.*"code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$body_file" 2>/dev/null | head -1)"
  printf '%s' "${code:--}"
}

record_id_of() {
  local body_file="$1" rid
  rid="$(sed -n 's/.*"record_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$body_file" 2>/dev/null | head -1)"
  printf '%s' "${rid:--}"
}

# do_push issues one push and emits its row.
#
# ERREXIT — LOAD-BEARING. A rejected push is the expected case for the
# unknown-kind class in EVERY phase, so curl's exit status is absorbed with
# `|| true` and the timed region survives it. Without that, `set -e` would
# abort the probe on exactly the responses the experiment exists to observe.
do_push() {
  local class="$1" kind="$2" semver="$3" tick="$4" payload="$5"
  local idem body w code ms observed
  idem="${RUN_ID}-${PHASE}-${class}-$(printf '%03d' "$tick")"
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  body="$TMPDIR_PROBE/body"
  w="$(curl -sS -o "$body" -w '%{http_code} %{time_total}' \
    --connect-timeout "$CONNECT_TIMEOUT" --max-time "$MAX_TIME" \
    -H "@$HDR_FILE" -X POST "$HTTP_BASE/v1/evidence:push" \
    -d "{\"record\":{\"idempotency_key\":\"$idem\",\"evidence_kind\":\"$kind\",\"schema_version\":\"$semver\",\"control_id\":\"$CONTROL_ID\",\"scope\":[{\"key\":\"env\",\"values\":[\"prod\"]}],\"observed_at\":\"$observed\",\"result\":\"pass\",\"payload\":$payload,\"source_attribution\":{\"actor_type\":\"service\",\"actor_id\":\"chaos-358\"}}}" \
    2>>"$TMPDIR_PROBE/curl.err" || true)"
  code="${w%% *}"
  ms="$(awk -v s="${w##* }" 'BEGIN{printf "%d", s*1000}' 2>/dev/null || printf '0')"
  [[ -n "$code" ]] || code="000"
  emit "$(date +%s)	$PHASE	$tick	$class	$idem	$code	$(err_code_of "$body")	$ms	$(record_id_of "$body")"
}

# do_get issues one registry read and emits its row. `idem_key` is `-`:
# a read has no record to correlate against the ledger.
do_get() {
  local class="$1" path="$2" tick="$3"
  local body w code ms
  body="$TMPDIR_PROBE/body"
  w="$(curl -sS -o "$body" -w '%{http_code} %{time_total}' \
    --connect-timeout "$CONNECT_TIMEOUT" --max-time "$MAX_TIME" \
    -H "@$HDR_FILE" "$HTTP_BASE$path" 2>>"$TMPDIR_PROBE/curl.err" || true)"
  code="${w%% *}"
  ms="$(awk -v s="${w##* }" 'BEGIN{printf "%d", s*1000}' 2>/dev/null || printf '0')"
  [[ -n "$code" ]] || code="000"
  emit "$(date +%s)	$PHASE	$tick	$class	-	$code	$(err_code_of "$body")	$ms	-"
}

START="$(date +%s)"
for ((i = 0; i < DURATION_SECONDS; i++)); do
  do_push known "$KNOWN_KIND" "$KNOWN_SEMVER" "$i" \
    "{\"review_id\":\"${RUN_ID}-${PHASE}-${i}\",\"completed_by\":\"chaos-358\",\"reviewer_role\":\"security\",\"users_reviewed\":$((i % 20 + 1))}"
  do_push unknown "$UNKNOWN_KIND" "1.0.0" "$i" '{"probe":"unknown"}'
  if [[ "$i" -eq "$COLD_AT_TICK" ]]; then
    do_push cold "$COLD_KIND" "1.0.0" "$i" '{"probe":"cold"}'
  fi
  do_get registry_list "/v1/schemas?limit=5" "$i"
  do_get registry_get "/v1/schemas/$KNOWN_KIND/$KNOWN_SEMVER" "$i"

  # Absolute-boundary scheduling: sleep to (START + i + 1), not a fixed 1s,
  # so per-tick request cost does not accumulate into cadence drift.
  target=$((START + i + 1))
  now="$(date +%s)"
  if [[ "$now" -lt "$target" ]]; then sleep $((target - now)); fi
done
