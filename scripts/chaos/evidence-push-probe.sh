#!/usr/bin/env bash
#
# evidence-push-probe.sh — the synthetic SDK push client for slice 335
# Experiment 5 ("atlas container restart mid-evidence-push"), executed by
# slice 356b.
#
# The design's steady state is "synthetic SDK client pushes records at 1/s
# for 60 seconds". This probe is that client. Each tick shells out to
# `atlas-cli evidence push`, which is the SHIPPED SDK path — cmd/atlas-cli
# root.go `newSDKClient()` → `pkg/sdk-go`.Client.Push → the
# EvidenceIngestService gRPC RPC. Nothing here re-implements the wire
# protocol, so what the probe measures is the SDK contract itself and not
# a harness approximation of it.
#
# TWO RETRY MODES — LOAD-BEARING, and the reason this probe exists rather
# than a one-line loop.
#
#   --retry-mode none    One attempt per record. This is `pkg/sdk-go` AS
#                        SHIPPED: Client.Push issues exactly one RPC and
#                        wraps the error. There is no retry, no backoff,
#                        and no attempt counter anywhere in the package.
#
#   --retry-mode design  The backoff the slice 335 design attributes to
#                        the SDK — "1s, 2s, 4s, 8s with jitter per slice
#                        003 spec" — applied by the CALLER, around the
#                        same single-shot Push. Same idempotency key and
#                        byte-identical content on every attempt, which is
#                        what makes the ledger's dedup the thing under
#                        test.
#
# Running both arms is what separates the design's two conjuncts. The
# hypothesis claims (a) the SDK re-sends, and (b) the idempotency key
# prevents duplicates. `none` measures (a) against the shipped code;
# `design` measures (b) without letting a failure of (a) mask it.
#
# RETRY CLASSIFICATION. Only transport-class codes are retried
# (Unavailable, DeadlineExceeded, Internal, Unknown). AlreadyExists is
# terminal by construction: the ledger returns it for a same-key push with
# DIFFERENT content, so retrying it would be retrying a client bug. Every
# other code is a rejection, not a transient — retrying inflates the
# attempt count without changing the outcome.
#
# CONTENT STABILITY ACROSS RETRIES — LOAD-BEARING. `observed_at` and the
# payload are computed ONCE per tick, before the first attempt, and reused
# verbatim. Recomputing `observed_at` per attempt would change the
# canonical hash, and the ledger would answer the retry with AlreadyExists
# (same key, different content) instead of the original receipt — turning
# a correct dedup into a spurious falsification.
#
# CADENCE. Ticks fire on absolute one-second boundaries from the run's
# start, each in the background, so a tick spending 15 seconds in backoff
# does not push the following ticks off schedule. `none` mode therefore
# still emits exactly one record per second while the container is down.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 356
# P0-1) — LOAD-BEARING. The gRPC endpoint is refused unless its host is a
# loopback literal. This probe cannot be aimed at atlas-edge, a hosted
# tenant, or any other host on the network.
#
# SECRETS. The bearer is read from a 0600 file named by --bearer-file and
# exported into the child's environment. It never appears on argv, which
# is world-readable via `ps` on this platform.
#
# OUTPUT — one TSV row per ATTEMPT (not per record), appended atomically:
#
#   epoch_s  phase  tick  idem_key  attempt  outcome  code  wall_ms  record_id  hash
#
# `outcome` is ok | err. `code` is the gRPC status code parsed from the
# CLI's error text, or `none` on success, or `unparsed` when the CLI
# failed without a recognizable status. `wall_ms` is END-TO-END CLI wall
# time — process start, gRPC dial, RPC, and exit. It is NOT the platform's
# ack latency; see the harness-floor note in the orchestrator.
#
# Usage:
#   scripts/chaos/evidence-push-probe.sh \
#     --atlas-cli ./atlas-cli --bearer-file ./bearer \
#     --control <uuid> --run-id steady --phase steady_state \
#     --duration-seconds 60 --retry-mode none --out steady.tsv
#
# Exit: 0 completed, 1 refused (scope guard tripped), 2 environment error.

set -Eeuo pipefail

GRPC_ENDPOINT="127.0.0.1:50051"
DURATION_SECONDS=60
RETRY_MODE="none"
PHASE="run"
RUN_ID=""
CONTROL_ID=""
BEARER_FILE=""
ATLAS_CLI=""
OUT=""
MAX_INFLIGHT=24
# The design's schedule, verbatim. Jitter is applied per delay below.
BACKOFF_SCHEDULE=(1 2 4 8)

die() {
  echo "evidence-push-probe: $1" >&2
  exit "${2:-2}"
}

is_uint() { [[ "$1" =~ ^[0-9]+$ ]]; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --endpoint)
    GRPC_ENDPOINT="${2:-}"
    shift 2
    ;;
  --duration-seconds)
    DURATION_SECONDS="${2:-}"
    shift 2
    ;;
  --retry-mode)
    RETRY_MODE="${2:-}"
    shift 2
    ;;
  --phase)
    PHASE="${2:-}"
    shift 2
    ;;
  --run-id)
    RUN_ID="${2:-}"
    shift 2
    ;;
  --control)
    CONTROL_ID="${2:-}"
    shift 2
    ;;
  --bearer-file)
    BEARER_FILE="${2:-}"
    shift 2
    ;;
  --atlas-cli)
    ATLAS_CLI="${2:-}"
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
[[ -n "$CONTROL_ID" ]] || die "--control is required"
[[ -n "$BEARER_FILE" ]] || die "--bearer-file is required"
[[ -f "$BEARER_FILE" ]] || die "bearer file not found: $BEARER_FILE"
[[ -n "$ATLAS_CLI" ]] || die "--atlas-cli is required"
[[ -x "$ATLAS_CLI" ]] || die "atlas-cli not executable: $ATLAS_CLI"
is_uint "$DURATION_SECONDS" || die "--duration-seconds must be a non-negative integer"
[[ "$RUN_ID" =~ ^[A-Za-z0-9_-]+$ ]] || die "--run-id must be [A-Za-z0-9_-]+"
case "$RETRY_MODE" in
none | design) ;;
*) die "--retry-mode must be 'none' or 'design'" ;;
esac

# --- scope guard: loopback gRPC endpoint -------------------------------
_host="${GRPC_ENDPOINT%:*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "evidence-push-probe: REFUSED — endpoint host '$_host' is not loopback." >&2
  echo "  Slice 335 restricts every experiment to the local docker-compose" >&2
  echo "  stack (P0-335-2 / slice 356 P0-1). Refusing to proceed." >&2
  exit 1
  ;;
esac

SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT"
SECURITY_ATLAS_TOKEN="$(cat "$BEARER_FILE")"
export SECURITY_ATLAS_ENDPOINT SECURITY_ATLAS_TOKEN
[[ -n "$SECURITY_ATLAS_TOKEN" ]] || die "bearer file $BEARER_FILE is empty"

: >"$OUT"
TIMEFORMAT='%3R'

# emit appends one already-formed TSV line. Single writes of this size are
# atomic under PIPE_BUF, so background ticks cannot interleave a row.
emit() { printf '%s\n' "$1" >>"$OUT"; }

# grpc_code extracts the status code from the CLI's error text, which has
# the shape `rpc error: code = Unavailable desc = ...`.
grpc_code() {
  local text="$1" code
  code="$(printf '%s' "$text" | sed -n 's/.*code = \([A-Za-z]*\).*/\1/p' | head -1)"
  if [[ -n "$code" ]]; then printf '%s' "$code"; else printf 'unparsed'; fi
}

is_retryable() {
  case "$1" in
  Unavailable | DeadlineExceeded | Internal | Unknown) return 0 ;;
  *) return 1 ;;
  esac
}

# jittered echoes a delay with +/-25% jitter, in whole tenths of a second.
# The design says "with jitter" without naming a distribution; +/-25% is
# the conventional shape and is recorded here rather than left implicit.
jittered() {
  local base="$1" tenths spread
  tenths=$((base * 10))
  spread=$((tenths / 4))
  # RANDOM in [0, 2*spread] recentred to [-spread, +spread].
  printf '%s' "$(awk -v t="$((tenths - spread + RANDOM % (2 * spread + 1)))" 'BEGIN{printf "%.1f", t/10}')"
}

# push_once runs one attempt and echoes: outcome<TAB>code<TAB>wall_ms<TAB>record_id<TAB>hash
#
# ERREXIT — LOAD-BEARING. A failed push is the NORMAL case during the
# injection window, so the timed region must survive a non-zero CLI exit.
# `set -e` is disabled inside the measurement subshell and the CLI's
# streams are routed to files rather than through an assignment: under
# errexit a failing `out="$(cli ...)"` aborts the subshell before the exit
# code is ever recorded, and the probe would then lose exactly the
# attempts the experiment exists to observe.
push_once() {
  local idem="$1" observed="$2" payload="$3"
  local out rc elapsed wall_ms record_id hash code
  local rcf="$TMPDIR_PROBE/rc.$idem" outf="$TMPDIR_PROBE/out.$idem"
  elapsed="$(
    set +e
    {
      time {
        "$ATLAS_CLI" evidence push --insecure \
          --kind access_review.completion.v1 --schema-version 1.0.0 \
          --control "$CONTROL_ID" --scope '{"env":"prod"}' \
          --observed-at "$observed" --result pass --payload "$payload" \
          --idempotency-key "$idem" \
          --actor-type service --actor-id "chaos-356b" >"$outf" 2>&1
        printf '%s\n' "$?" >"$rcf"
      }
    } 2>&1
  )"
  rc="$(cat "$rcf" 2>/dev/null || printf '99')"
  out="$(cat "$outf" 2>/dev/null || printf 'probe: CLI produced no output')"
  rm -f "$rcf" "$outf"
  wall_ms="$(awk -v s="$elapsed" 'BEGIN{printf "%d", s*1000}')"

  if [[ "$rc" == "0" ]]; then
    record_id="$(printf '%s' "$out" | sed -n 's/.*"recordId"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    hash="$(printf '%s' "$out" | sed -n 's/.*"hash"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    printf 'ok\tnone\t%s\t%s\t%s' "$wall_ms" "${record_id:-unparsed}" "${hash:-unparsed}"
  else
    code="$(grpc_code "$out")"
    printf 'err\t%s\t%s\t-\t-' "$code" "$wall_ms"
  fi
}

# tick runs one record end to end: one attempt in `none` mode, up to the
# design's four retries in `design` mode.
tick() {
  local n="$1"
  local idem observed payload attempt result outcome code delay
  idem="$(printf '%s-%03d' "$RUN_ID" "$n")"
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  payload="{\"review_id\":\"$idem\",\"completed_by\":\"chaos-356b\",\"reviewer_role\":\"security\",\"users_reviewed\":$((n % 20 + 1))}"

  attempt=0
  while :; do
    attempt=$((attempt + 1))
    result="$(push_once "$idem" "$observed" "$payload")"
    outcome="$(printf '%s' "$result" | cut -f1)"
    code="$(printf '%s' "$result" | cut -f2)"
    emit "$(date +%s)	$PHASE	$n	$idem	$attempt	$result"

    [[ "$outcome" == "ok" ]] && break
    [[ "$RETRY_MODE" == "design" ]] || break
    is_retryable "$code" || break
    [[ $attempt -le ${#BACKOFF_SCHEDULE[@]} ]] || break

    delay="$(jittered "${BACKOFF_SCHEDULE[$((attempt - 1))]}")"
    sleep "$delay"
  done
}

TMPDIR_PROBE="$(mktemp -d)"
cleanup() {
  wait
  rm -rf "$TMPDIR_PROBE"
}
trap cleanup EXIT

START="$(date +%s)"
for ((i = 0; i < DURATION_SECONDS; i++)); do
  # Bound concurrent in-flight ticks. `design` mode can hold a tick open
  # for the full backoff schedule, so an unbounded fan-out would spawn a
  # process storm during a long outage.
  while [[ "$(jobs -pr | wc -l | tr -d ' ')" -ge "$MAX_INFLIGHT" ]]; do sleep 0.2; done
  tick "$i" &
  # Absolute-boundary scheduling: sleep to (START + i + 1), not a fixed
  # 1s, so per-tick spawn cost does not accumulate into cadence drift.
  target=$((START + i + 1))
  now="$(date +%s)"
  if [[ "$now" -lt "$target" ]]; then sleep $((target - now)); fi
done
wait
