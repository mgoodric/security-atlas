#!/usr/bin/env bash
#
# nats-consumer-snapshot.sh — capture the JetStream stream + consumer state
# that slice 335 Experiment 2 (NATS consumer lag spike) requires, executed by
# slice 355.
#
# Satisfies two of the design's three pre-execution checklist items:
#
#   [ ] Confirm the durable consumer's `ack_wait` is > 10 minutes
#   [ ] Snapshot the eval consumer's config BEFORE pausing
#
# It also doubles as the per-second sampler for the injection window: with
# --repeat-seconds it appends one CSV row per sample so consumer-pending count
# and ack-floor progression are recorded across the whole run.
#
# SOURCE OF TRUTH: the NATS monitoring endpoint (`/jsz`) on the LOCAL compose
# stack. No NATS CLI binary is required, so this script adds no dependency to
# the repo (slice 355 P0-2). Read-only — it never mutates stream or consumer
# state; the injection lives in nats-consumer-pause.sh.
#
# SCOPE BOUNDARY (slice 335 P0-335-2 / slice 355 P0-1) — LOAD-BEARING.
# The monitor URL is refused unless its host is a loopback literal
# (127.0.0.1 / localhost / ::1). This tool cannot be pointed at atlas-edge,
# a hosted tenant, or any other host on the network.
#
# NAME RECONCILIATION. The slice 335 design names the stream `atlas_eval` and
# the consumer `evidence-evaluator`. Those names are design-time placeholders;
# the shipped code uses `EVIDENCE_INGEST` (streambuf.DefaultStreamName) and
# `evidence_eval_worker` (eval.EvalConsumerDurable). The shipped names are the
# defaults here. See the decisions log D1.
#
# Usage:
#   scripts/chaos/nats-consumer-snapshot.sh --out snapshot.json
#   scripts/chaos/nats-consumer-snapshot.sh --csv samples.csv \
#     --repeat-seconds 1200 --interval 1
#
# Exit: 0 captured, 1 refused (guard tripped), 2 env error.

set -eu

MONITOR_URL="http://127.0.0.1:8222"
STREAM="EVIDENCE_INGEST"
CONSUMER="evidence_eval_worker"
OUT=""
CSV=""
REPEAT_SECONDS=0
INTERVAL=1

die() {
  echo "nats-consumer-snapshot: $1" >&2
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
  --monitor-url)
    MONITOR_URL="${2:-}"
    shift 2
    ;;
  --stream)
    STREAM="${2:-}"
    shift 2
    ;;
  --consumer)
    CONSUMER="${2:-}"
    shift 2
    ;;
  --out)
    OUT="${2:-}"
    shift 2
    ;;
  --csv)
    CSV="${2:-}"
    shift 2
    ;;
  --repeat-seconds)
    REPEAT_SECONDS="${2:-}"
    shift 2
    ;;
  --interval)
    INTERVAL="${2:-}"
    shift 2
    ;;
  -h | --help)
    sed -n '2,40p' "$0"
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

# ---------------------------------------------------------------------------
# Guard — loopback-only target. Anything else is refused outright.
# ---------------------------------------------------------------------------
case "$MONITOR_URL" in
"http://127.0.0.1" | "http://localhost") : ;;
"http://127.0.0.1:"* | "http://localhost:"*) : ;;
"http://[::1]" | "http://[::1]:"*) : ;;
*) die "monitor URL '$MONITOR_URL' is not loopback; this experiment is local-compose ONLY" 1 ;;
esac
# A path or userinfo component would let the loopback prefix be spoofed
# (e.g. http://localhost:8222@edge.example.com/...). Refuse both.
case "${MONITOR_URL#http://}" in
*[/@]*) die "monitor URL must be scheme://host[:port] with no path or userinfo" 1 ;;
esac

case "$STREAM$CONSUMER" in
*[!A-Za-z0-9_.-]*) die "stream/consumer names must be [A-Za-z0-9_.-]" ;;
esac
is_uint "$REPEAT_SECONDS" || die "--repeat-seconds must be a non-negative integer"
is_uint "$INTERVAL" || [ "$INTERVAL" = "0" ] || die "--interval must be a positive integer"
[ "$INTERVAL" -gt 0 ] 2>/dev/null || INTERVAL=1

command -v curl >/dev/null 2>&1 || die "curl not found on PATH"
command -v jq >/dev/null 2>&1 || die "jq not found on PATH"

JSZ="$MONITOR_URL/jsz?consumers=true&config=true&streams=true"

# fetch_raw prints the /jsz document for the configured stream + consumer, or
# an empty object when the endpoint is unreachable. Never fatal: during the
# injection window the sampler must keep sampling across a transient blip
# rather than tearing the run down.
fetch_raw() {
  curl -s --max-time 5 "$JSZ" 2>/dev/null |
    jq -c --arg s "$STREAM" --arg c "$CONSUMER" '
      [ .account_details[]?.stream_detail[]? | select(.name == $s) ] | .[0] // {} |
      {
        stream: .name,
        stream_msgs:    (.state.messages // 0),
        stream_first:   (.state.first_seq // 0),
        stream_last:    (.state.last_seq // 0),
        consumer: ([ .consumer_detail[]? | select(.name == $c) ] | .[0] // {})
      } |
      {
        stream:          (.stream // ""),
        stream_msgs:     .stream_msgs,
        stream_last_seq: .stream_last,
        consumer:        (.consumer.name // ""),
        ack_wait_ns:     (.consumer.config.ack_wait // 0),
        max_ack_pending: (.consumer.config.max_ack_pending // 0),
        paused:          (.consumer.paused // false),
        pause_remaining: (.consumer.pause_remaining // 0),
        num_pending:     (.consumer.num_pending // 0),
        num_ack_pending: (.consumer.num_ack_pending // 0),
        num_redelivered: (.consumer.num_redelivered // 0),
        delivered_seq:   (.consumer.delivered.stream_seq // 0),
        ack_floor_seq:   (.consumer.ack_floor.stream_seq // 0)
      }
    ' 2>/dev/null || echo '{}'
}

emit_csv_header() {
  echo "epoch_s,stream_msgs,stream_last_seq,num_pending,num_ack_pending,num_redelivered,delivered_seq,ack_floor_seq,paused" >"$1"
}

emit_csv_row() {
  _now="$(date -u +%s)"
  fetch_raw | jq -r --arg ts "$_now" '
    [$ts,
     (.stream_msgs|tostring), (.stream_last_seq|tostring),
     (.num_pending|tostring), (.num_ack_pending|tostring),
     (.num_redelivered|tostring),
     (.delivered_seq|tostring), (.ack_floor_seq|tostring),
     (.paused|tostring)] | join(",")
  ' 2>/dev/null || echo "$_now,ERR,ERR,ERR,ERR,ERR,ERR,ERR,ERR"
}

# ---------------------------------------------------------------------------
# One-shot snapshot mode (checklist items 1 + 2).
# ---------------------------------------------------------------------------
if [ -n "$OUT" ]; then
  _snap="$(fetch_raw)"
  _found="$(echo "$_snap" | jq -r '.consumer')"
  if [ "$_found" != "$CONSUMER" ]; then
    die "consumer '$CONSUMER' not found on stream '$STREAM' at $MONITOR_URL"
  fi
  _ackwait_ns="$(echo "$_snap" | jq -r '.ack_wait_ns')"
  _ackwait_s=$((_ackwait_ns / 1000000000))
  echo "$_snap" | jq --arg captured "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg ackwait_s "$_ackwait_s" \
    '. + {captured_at: $captured, ack_wait_seconds: ($ackwait_s|tonumber),
          ack_wait_gt_10min: (($ackwait_s|tonumber) > 600)}' >"$OUT"
  echo "nats-consumer-snapshot: wrote $OUT"
  echo "nats-consumer-snapshot: ack_wait=${_ackwait_s}s (checklist requires > 600s)"
  if [ "$_ackwait_s" -le 600 ]; then
    echo "nats-consumer-snapshot: CHECKLIST ITEM 1 NOT SATISFIED — ack_wait must be raised before injecting" >&2
  fi
fi

# ---------------------------------------------------------------------------
# Sampler mode — one row per --interval for --repeat-seconds.
# ---------------------------------------------------------------------------
if [ -n "$CSV" ] && [ "$REPEAT_SECONDS" -gt 0 ]; then
  [ -f "$CSV" ] || emit_csv_header "$CSV"
  _end=$(($(date -u +%s) + REPEAT_SECONDS))
  while [ "$(date -u +%s)" -lt "$_end" ]; do
    _boundary="$(date -u +%s)"
    emit_csv_row >>"$CSV"
    while [ "$(date -u +%s)" -lt $((_boundary + INTERVAL)) ]; do
      sleep 0.1
    done
  done
  echo "nats-consumer-snapshot: sampler finished, rows in $CSV"
fi

if [ -z "$OUT" ] && [ -z "$CSV" ]; then
  fetch_raw | jq .
fi
