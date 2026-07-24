#!/usr/bin/env bash
#
# nats-consumer-pause.sh — the injection primitive for slice 335 Experiment 2
# (NATS JetStream consumer lag spike), executed by slice 355.
#
# Perturbs exactly ONE variable, as the design requires: the evaluation
# consumer's durable state, paused vs active. Ingest is never touched — the
# push API keeps accepting records and the slice-015 ledger-writer consumer
# (`evidence_ingest_worker`) keeps draining them. Only the slice-016
# evaluation reaction consumer (`evidence_eval_worker`) stops.
#
# Verbs:
#   ack-wait <duration>   set the eval consumer's ack_wait (checklist item 1)
#   pause <duration>      pause the eval consumer for <duration>
#   resume                resume it immediately
#   info                  print consumer info
#
# MECHANISM. The NATS CLI is run from the official `natsio/nats-box` image
# attached to the compose network, so nothing is installed on the host and no
# chaos framework enters the repo (slice 355 P0-2). Consumer pause requires
# NATS Server 2.11 — see scripts/chaos/compose.chaos-nats-211.yml.
#
# SCOPE BOUNDARY (slice 335 P0-335-2 / slice 355 P0-1) — LOAD-BEARING.
# The NATS server address is not configurable to an arbitrary host: this
# script always talks to `nats://nats:4222` FROM INSIDE a container attached
# to the named local compose network. There is no argument that can redirect
# it at atlas-edge, a hosted tenant, or any host outside this machine's
# compose stack. The only tunable is which local compose network to join,
# and that value is rejected unless it looks like a compose-managed network.
#
# Usage:
#   scripts/chaos/nats-consumer-pause.sh --network security-atlas_default info
#   scripts/chaos/nats-consumer-pause.sh ack-wait 15m
#   scripts/chaos/nats-consumer-pause.sh pause 10m
#   scripts/chaos/nats-consumer-pause.sh resume
#
# Exit: 0 ok, 1 refused (guard tripped), 2 env error, 4 unsupported by server.

set -eu

NETWORK="security-atlas_default"
STREAM="EVIDENCE_INGEST"
CONSUMER="evidence_eval_worker"
NATS_BOX_IMAGE="natsio/nats-box:latest"
# In-network address only. Deliberately NOT a CLI flag — see scope boundary.
NATS_ADDR="nats://nats:4222"

die() {
  echo "nats-consumer-pause: $1" >&2
  exit "${2:-2}"
}

while [ $# -gt 0 ]; do
  case "$1" in
  --network)
    NETWORK="${2:-}"
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
  -h | --help)
    sed -n '2,38p' "$0"
    exit 0
    ;;
  *) break ;;
  esac
done

VERB="${1:-info}"
ARG="${2:-}"

case "$STREAM$CONSUMER$NETWORK" in
*[!A-Za-z0-9_.-]*) die "stream/consumer/network names must be [A-Za-z0-9_.-]" ;;
esac

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"

# The network must already exist locally AND be a compose-managed bridge on
# this host. A network that does not exist here cannot be a remote target,
# but checking makes the local-only posture explicit rather than incidental.
if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
  die "network '$NETWORK' does not exist on this host; this experiment is local-compose ONLY" 1
fi
_driver="$(docker network inspect "$NETWORK" --format '{{.Driver}}' 2>/dev/null || echo "")"
case "$_driver" in
bridge) : ;;
*) die "network '$NETWORK' has driver '$_driver'; only a local bridge network is permitted" 1 ;;
esac

natsbox() {
  docker run --rm --network "$NETWORK" "$NATS_BOX_IMAGE" nats -s "$NATS_ADDR" "$@"
}

case "$VERB" in
info)
  natsbox consumer info "$STREAM" "$CONSUMER"
  ;;

ack-wait)
  [ -n "$ARG" ] || die "ack-wait requires a duration, e.g. 15m"
  case "$ARG" in
  *[!0-9smh]*) die "ack-wait duration must be like 60s / 15m / 1h" ;;
  esac
  echo "nats-consumer-pause: setting ack_wait=$ARG on $STREAM/$CONSUMER"
  natsbox consumer edit "$STREAM" "$CONSUMER" --ack-wait="$ARG" --force
  ;;

pause)
  [ -n "$ARG" ] || die "pause requires a duration, e.g. 10m"
  case "$ARG" in
  *[!0-9smh]*) die "pause duration must be like 30s / 10m / 1h" ;;
  esac
  echo "nats-consumer-pause: INJECT pause $ARG on $STREAM/$CONSUMER at $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  # `nats consumer pause <stream> <consumer> <duration>` takes a duration and
  # the server auto-resumes at expiry. The explicit `resume` verb below is the
  # design's rollback step and is run regardless, so a partial run never
  # leaves the consumer paused longer than the experiment window.
  _out="$(natsbox consumer pause "$STREAM" "$CONSUMER" "$ARG" --force 2>&1 || true)"
  echo "$_out"
  case "$_out" in
  *"requires NATS Server 2.11"*)
    die "server does not support consumer pause (needs NATS 2.11); see scripts/chaos/compose.chaos-nats-211.yml" 4
    ;;
  esac
  # `nats consumer pause` prints the paused-until timestamp on success. Treat
  # anything that did not report a pause as a failed injection rather than
  # silently proceeding to measure a window in which nothing was perturbed.
  case "$_out" in
  *[Pp]aused*) : ;;
  *) die "pause did not take effect; output was: $_out" 4 ;;
  esac
  ;;

resume)
  echo "nats-consumer-pause: ROLLBACK resume $STREAM/$CONSUMER at $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  natsbox consumer resume "$STREAM" "$CONSUMER" --force
  ;;

*)
  die "unknown verb '$VERB' (expected: info | ack-wait | pause | resume)"
  ;;
esac
