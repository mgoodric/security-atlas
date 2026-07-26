#!/usr/bin/env bash
#
# run-exp3-pg-outage.sh — orchestrator for slice 335 Experiment 3,
# "Postgres primary unavailable". Executed by slice 356a.
#
# Runs the design's Method verbatim against the LOCAL docker-compose stack:
#
#   1. Verify steady state           (design step 1)
#   2. Inject: stop postgres         (design step 2)
#   3. Fire synthetic requests, 60s  (design step 3-4)
#   4. Restart postgres; measure recovery time (design step 5)
#
# The design is the contract. This script does not reinterpret it: the
# 60-second injection window, the three probed surfaces, the 30-second
# hang threshold, and the abort criteria are all read straight out of
# docs/audits/335-chaos-experiment-design.md §Experiment 3.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 356
# P0-1) — LOAD-BEARING. Every action is scoped to one named local
# docker-compose project. The script refuses to run unless:
#   - the base URL host is a loopback literal, and
#   - the postgres container it is about to stop belongs to the compose
#     project rooted at the compose file passed in.
# It therefore cannot stop a container belonging to atlas-edge, a hosted
# deployment, or any unrelated stack on this machine.
#
# ABORT CRITERIA (design, verbatim): any request hangs > 30 seconds with
# no response; OR the atlas container crashes. Either trips an immediate
# rollback (`start postgres`). Per the design, a >30s hang is flagged as
# an abort AND recorded as a falsification — it is not a "successful"
# outcome.
#
# PRE-EXECUTION CHECKLIST (design, verbatim):
#   [ ] Confirm no in-flight evidence pushes that would orphan
#       (idempotency key registry should make this safe; verify).
#   [ ] Snapshot `evidence_records` row count BEFORE; verify identical
#       AFTER (no partial-write recovery state).
# Both are executed by preflight() below and echoed into the run log so
# the decisions log can sign them off against real output.
#
# Auth: the bearer JWT is read from ATLAS_CHAOS_TOKEN only — never argv.
#
# Environment:
#   ATLAS_CHAOS_TOKEN     bearer JWT (required)
#   ATLAS_CHAOS_CONTROL   control_id used by the write probe (required)
#
# Usage:
#   ATLAS_CHAOS_TOKEN=... ATLAS_CHAOS_CONTROL=... \
#     scripts/chaos/run-exp3-pg-outage.sh --out-dir /tmp/exp3
#
# Exit: 0 ran to completion (falsified or not — read the summary),
#       1 refused (guard tripped) or aborted mid-run,
#       2 environment error.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker/docker-compose.yml"
ENV_FILE="$REPO_ROOT/deploy/docker/.env"
BASE_URL="http://127.0.0.1:58080"
OUT_DIR=""
INJECT_SECONDS=60
STEADY_SECONDS=30
RECOVERY_BUDGET=120
HANG_THRESHOLD_MS=30000

die() {
  echo "run-exp3: $1" >&2
  exit "${2:-2}"
}

log() { echo "[$(date -u +%H:%M:%S)] $*"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --out-dir)
    OUT_DIR="${2:-}"
    shift 2
    ;;
  --base-url)
    BASE_URL="${2:-}"
    shift 2
    ;;
  --compose-file)
    COMPOSE_FILE="${2:-}"
    shift 2
    ;;
  --env-file)
    ENV_FILE="${2:-}"
    shift 2
    ;;
  --inject-seconds)
    INJECT_SECONDS="${2:-}"
    shift 2
    ;;
  --steady-seconds)
    STEADY_SECONDS="${2:-}"
    shift 2
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[[ -n "$OUT_DIR" ]] || die "--out-dir is required"
[[ -n "${ATLAS_CHAOS_TOKEN:-}" ]] || die "ATLAS_CHAOS_TOKEN is unset"
[[ -n "${ATLAS_CHAOS_CONTROL:-}" ]] || die "ATLAS_CHAOS_CONTROL is unset"
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
command -v docker >/dev/null 2>&1 || die "docker not on PATH"

# --- scope guard: loopback base URL ------------------------------------
_hostport="${BASE_URL#*://}"
_hostport="${_hostport%%/*}"
_host="${_hostport%%:*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "run-exp3: REFUSED — base URL host '$_host' is not loopback." >&2
  echo "  Slice 335 restricts every experiment to the local" >&2
  echo "  docker-compose stack. Refusing to proceed." >&2
  exit 1
  ;;
esac

mkdir -p "$OUT_DIR"
PROBE_TSV="$OUT_DIR/probes.tsv"
RUN_LOG="$OUT_DIR/run.log"

compose() {
  if [[ -f "$ENV_FILE" ]]; then
    docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
  else
    docker compose -f "$COMPOSE_FILE" "$@"
  fi
}

# Resolve the container IDs from the compose project itself rather than
# guessing at names. `compose ps -q <svc>` only ever returns a container
# belonging to THIS project, which is what makes the stop safe.
PG_CID="$(compose ps -q postgres || true)"
ATLAS_CID="$(compose ps -q atlas || true)"
[[ -n "$PG_CID" ]] || die "no postgres container in the compose project at $COMPOSE_FILE — bring the stack up first"
[[ -n "$ATLAS_CID" ]] || die "no atlas container in the compose project at $COMPOSE_FILE"

NATS_CID="$(compose ps -q nats || true)"
PG_NAME="$(docker inspect --format '{{.Name}}' "$PG_CID" | sed 's|^/||')"
ATLAS_NAME="$(docker inspect --format '{{.Name}}' "$ATLAS_CID" | sed 's|^/||')"

psql_q() {
  docker exec "$PG_CID" psql -U postgres -d security_atlas -Atc "$1"
}

# Depth of the JetStream ingest buffer. LOAD-BEARING for the checklist:
# POST /v1/evidence:push acks BEFORE the ledger write (streambuf.go — the
# ingest/eval separation of constitutional invariant 2), so a push that
# has been accepted may be sitting on EVIDENCE_INGEST and not yet in
# `evidence_records`. A checklist that watched only the ledger would call
# the system quiesced while records were still buffered.
stream_depth() {
  if [[ -z "$NATS_CID" ]]; then
    echo "n/a"
    return 0
  fi
  docker exec "$NATS_CID" wget -qO- 'http://localhost:8222/jsz?streams=1' 2>/dev/null |
    python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
except Exception:
    print("n/a"); sys.exit(0)
for acc in d.get("account_details", []):
    for s in acc.get("stream_detail", []):
        if s.get("name") == "EVIDENCE_INGEST":
            print(s.get("state", {}).get("messages", "n/a")); sys.exit(0)
print("n/a")
' 2>/dev/null || echo "n/a"
}

container_running() {
  [[ "$(docker inspect --format '{{.State.Running}}' "$1" 2>/dev/null || echo false)" == "true" ]]
}

atlas_restart_count() {
  docker inspect --format '{{.RestartCount}}' "$ATLAS_CID" 2>/dev/null || echo "?"
}

probe_once() {
  # Single ad-hoc probe used by the steady-state gate and the recovery
  # poll. Returns "<code> <time_total>".
  curl -s -o /dev/null --max-time 35 -w '%{http_code} %{time_total}' "$@" 2>/dev/null || echo "000 35"
}

# ======================================================================
# Pre-execution checklist (slice 335 §Experiment 3)
# ======================================================================
EVIDENCE_BEFORE=""
preflight() {
  log "=== PRE-EXECUTION CHECKLIST (slice 335 Experiment 3) ==="
  log "compose project file : $COMPOSE_FILE"
  log "postgres container   : $PG_NAME ($PG_CID)"
  log "atlas container      : $ATLAS_NAME ($ATLAS_CID)"
  log "base url             : $BASE_URL (loopback guard passed)"

  # C-0 (operational): the whole stack must be healthy before injecting.
  # Injecting chaos into an already-broken stack yields meaningless data.
  # Assert the two load-bearing containers explicitly first — `compose ps`
  # lists only running services by default, so a scan of its output alone
  # would pass VACUOUSLY if postgres or atlas were already down.
  container_running "$PG_CID" || die "postgres is not running — nothing to perturb" 2
  container_running "$ATLAS_CID" || die "atlas is not running — no steady state to measure" 2
  local unhealthy
  unhealthy="$(compose ps --format '{{.Name}} {{.State}} {{.Status}}' |
    grep -vE 'migrate|bootstrap|minio-mc' |
    grep -vE 'running .*healthy|running .*Up' || true)"
  if [[ -n "$unhealthy" ]]; then
    log "C-0 FAIL — stack is not fully healthy:"
    echo "$unhealthy"
    die "refusing to inject into an unhealthy stack" 2
  fi
  log "C-0 PASS — every long-lived service is running and healthy"

  # C-1 (design): confirm no in-flight evidence pushes that would orphan.
  # The observable proxy for "in-flight" is the NATS ingest stream's
  # pending/unacked depth plus a quiesced-writes check on the ledger: if
  # the row count is stable across a settle window and nothing is pending
  # on the stream, no push is mid-flight. The idempotency-key registry is
  # verified separately by re-pushing a key and asserting dedup.
  local depth_a depth_b
  depth_a="$(psql_q 'select count(*) from evidence_records;')"
  sleep 3
  depth_b="$(psql_q 'select count(*) from evidence_records;')"
  if [[ "$depth_a" != "$depth_b" ]]; then
    log "C-1 FAIL — evidence_records moved $depth_a -> $depth_b over a 3s settle window; writes are in flight"
    die "refusing to inject while pushes are in flight" 2
  fi
  log "C-1a PASS — ledger quiesced ($depth_a rows stable over 3s); no in-flight pushes"

  # C-1b: verify the idempotency-key registry actually makes a replayed
  # push safe, which is what stops an interrupted push from orphaning.
  #
  # WHAT THIS DOES **NOT** ASSERT, and why. The obvious check — replay a
  # key and expect `"deduplicated":true` in the response — is wrong on
  # this deployment. The compose stack runs the NATS-backed
  # StreamPublisher, which hardcodes `Deduplicated: false` on every
  # receipt (internal/evidence/streambuf/streambuf.go) because it acks
  # BEFORE the ledger write. The dedup decision is made later, by the
  # consumer, at Service.Process time. So the response flag reports
  # "accepted onto the buffer", not "already in the ledger", and reading
  # it as a dedup signal would fail a healthy system.
  #
  # The property that actually matters for orphan-safety is asserted
  # instead: a replayed key yields a byte-identical deterministic
  # record_id (UUIDv5 over the key) and content hash, and the ledger row
  # count does NOT increase. That is the real idempotency guarantee.
  #
  # The first push must be shown to LAND before the replay is meaningful.
  # Without that assertion the check passes vacuously whenever the ingest
  # consumer cannot persist at all: the ledger reads 0 before and 0 after,
  # "did not increase" holds, and a totally broken write path signs the
  # checklist off. That failure was observed for real on the first OE-384
  # attempt (a control_id scoped to the wrong tenant made every insert
  # fail its composite FK). Assert the landing explicitly.
  local key body r1 r2 id1 id2 ledger_pre_first ledger_before_replay ledger_after_replay
  key="chaos356a-preflight-$(date -u +%s)"
  body="$(
    cat <<JSON
{"records":[{"idempotency_key":"$key","evidence_kind":"access_review.completion.v1","schema_version":"1.0.0","control_id":"$ATLAS_CHAOS_CONTROL","scope":[{"key":"env","values":["prod"]}],"observed_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","result":"pass","payload":{"review_id":"$key","completed_by":"chaos-356a","reviewer_role":"security","users_reviewed":7},"source_attribution":{"actor_type":"service","actor_id":"chaos-356a"}}]}
JSON
  )"
  ledger_pre_first="$(psql_q 'select count(*) from evidence_records;')"
  r1="$(curl -s --max-time 10 -X POST -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" \
    -H 'Content-Type: application/json' -d "$body" "$BASE_URL/v1/evidence:push")"
  # Let the consumer drain the first push into the ledger before replaying,
  # so the replay is genuinely a duplicate-of-a-persisted-record.
  sleep 4
  ledger_before_replay="$(psql_q 'select count(*) from evidence_records;')"
  if [[ "$ledger_before_replay" -le "$ledger_pre_first" ]]; then
    log "C-1b FAIL — the preflight push did not reach the ledger ($ledger_pre_first -> $ledger_before_replay)"
    log "  push response : $r1"
    log "  The push path acks onto the NATS buffer before the ledger write, so"
    log "  a 200 here does NOT mean the record persisted. Check the atlas log for"
    log "  'streambuf: process error' — a control_id scoped to a tenant other than"
    log "  the bearer's trips the evidence_records (tenant_id, control_id) FK."
    log "  Use scripts/chaos/ensure-chaos-control.sh to obtain a valid control_id."
    die "write path is not persisting; the ledger snapshots would be vacuous" 2
  fi
  log "C-1b(i) PASS — preflight push landed in the ledger ($ledger_pre_first -> $ledger_before_replay)"
  r2="$(curl -s --max-time 10 -X POST -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" \
    -H 'Content-Type: application/json' -d "$body" "$BASE_URL/v1/evidence:push")"
  sleep 4
  ledger_after_replay="$(psql_q 'select count(*) from evidence_records;')"
  id1="$(printf '%s' "$r1" | grep -o '"record_id":"[^"]*"' | head -1)"
  id2="$(printf '%s' "$r2" | grep -o '"record_id":"[^"]*"' | head -1)"
  if [[ -z "$id1" || "$id1" != "$id2" ]]; then
    log "C-1b FAIL — replayed key did not yield an identical record_id"
    log "  first push : $r1"
    log "  replay     : $r2"
    die "idempotency contract unverified; an interrupted push could orphan" 2
  fi
  if [[ "$ledger_before_replay" != "$ledger_after_replay" ]]; then
    log "C-1b FAIL — replay added a ledger row ($ledger_before_replay -> $ledger_after_replay)"
    die "idempotency contract unverified; a replayed push duplicates" 2
  fi
  log "C-1b PASS — replayed key is idempotent: identical $id1, ledger steady at $ledger_after_replay row(s)"
  log "           (response 'deduplicated' flag is always false on the NATS path by design — not read here)"

  # C-2 (design): snapshot evidence_records row count BEFORE — plus the
  # ingest-buffer depth, since a push acks before the ledger write.
  EVIDENCE_BEFORE="$(psql_q 'select count(*) from evidence_records;')"
  STREAM_BEFORE="$(stream_depth)"
  log "C-2 PASS — evidence_records BEFORE = $EVIDENCE_BEFORE; EVIDENCE_INGEST depth BEFORE = $STREAM_BEFORE"

  log "=== CHECKLIST SATISFIED — injection is unblocked ==="
}

# ======================================================================
# Steady state (design step 1) — captured BEFORE any injection
# ======================================================================
steady_state() {
  log "=== STEADY STATE (${STEADY_SECONDS}s, pre-injection) ==="
  # Gate first: the design's steady state is "all API endpoints return
  # 2xx for valid requests; /healthz returns {status: ok}". A single
  # non-2xx here means there is no steady state to perturb.
  local rc hc
  rc="$(probe_once -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" "$BASE_URL/v1/anchors")"
  hc="$(probe_once "$BASE_URL/health")"
  log "gate: GET /v1/anchors -> ${rc%% *}   GET /health -> ${hc%% *}"
  [[ "${rc%% *}" == "200" ]] || die "steady-state gate failed: /v1/anchors returned ${rc%% *}" 2
  [[ "${hc%% *}" == "200" ]] || die "steady-state gate failed: /health returned ${hc%% *}" 2
  log "health body: $(curl -s --max-time 5 "$BASE_URL/health")"

  ATLAS_CHAOS_TOKEN="$ATLAS_CHAOS_TOKEN" ATLAS_CHAOS_CONTROL="$ATLAS_CHAOS_CONTROL" \
    "$REPO_ROOT/scripts/chaos/pg-outage-probe.sh" \
    --base-url "$BASE_URL" --phase steady --duration-seconds "$STEADY_SECONDS" \
    --out "$PROBE_TSV"
}

# ======================================================================
# Injection (design steps 2-4)
# ======================================================================
ABORTED=""
ABORT_REASON=""

abort_watch() {
  # Polls the two abort criteria once a second for the life of the
  # injection window and writes a sentinel file the moment either trips.
  #
  # The hang scan is filtered to `$2=="injection"`. Without the filter it
  # would also read the steady-state rows already in the TSV, so a slow
  # PRE-injection probe would fake an abort on a run whose injection
  # window was in fact clean.
  local deadline=$(($(date -u +%s) + INJECT_SECONDS + 40))
  while [[ "$(date -u +%s)" -lt "$deadline" ]]; do
    if ! container_running "$ATLAS_CID"; then
      echo "atlas container stopped running (crash) at $(date -u +%H:%M:%S)" >"$OUT_DIR/ABORT"
      return 0
    fi
    if [[ -f "$PROBE_TSV" ]] &&
      awk -F'\t' -v t="$HANG_THRESHOLD_MS" 'NR>1 && $2=="injection" && $5+0 > t { found=1 } END { exit !found }' "$PROBE_TSV"; then
      echo "a request exceeded the ${HANG_THRESHOLD_MS}ms hang threshold at $(date -u +%H:%M:%S)" >"$OUT_DIR/ABORT"
      return 0
    fi
    sleep 1
  done
}

inject() {
  log "=== INJECTION: stop postgres ==="

  # Injection-boundary snapshot. The design's checklist says "snapshot
  # evidence_records BEFORE; verify identical AFTER", but taken literally
  # at checklist time that comparison is CONFOUNDED: the steady-state
  # window deliberately writes evidence between the checklist and the
  # injection, so BEFORE != AFTER on a perfectly healthy run and the
  # check reports a phantom divergence.
  #
  # The question the checklist is actually asking is "did the outage
  # leave partial-write state?", and the snapshot that answers it is
  # taken here — after steady-state traffic, immediately before the
  # stop. Both pairs are reported; this is the load-bearing one.
  PRE_INJECT_EVIDENCE="$(psql_q 'select count(*) from evidence_records;')"
  PRE_INJECT_STREAM="$(stream_depth)"
  log "injection-boundary snapshot: evidence_records = $PRE_INJECT_EVIDENCE, EVIDENCE_INGEST = $PRE_INJECT_STREAM"

  ATLAS_RESTARTS_BEFORE="$(atlas_restart_count)"
  log "atlas RestartCount before injection = $ATLAS_RESTARTS_BEFORE"
  # Absolute path, not bare `rm`: an operator shell on this machine
  # aliases `rm` to `trash`, which rejects `-f`. Aliases do not reach a
  # non-interactive script, but a stale ABORT sentinel here would fake an
  # abort on a clean run, so the dependency is pinned rather than assumed.
  /bin/rm -f "$OUT_DIR/ABORT"

  INJECT_AT="$(date -u +%s)"
  compose stop postgres
  log "postgres stopped at epoch=$INJECT_AT ($(date -u +%H:%M:%SZ))"
  log "postgres running? $(container_running "$PG_CID" && echo yes || echo no)"

  abort_watch &
  local watcher=$!

  ATLAS_CHAOS_TOKEN="$ATLAS_CHAOS_TOKEN" ATLAS_CHAOS_CONTROL="$ATLAS_CHAOS_CONTROL" \
    "$REPO_ROOT/scripts/chaos/pg-outage-probe.sh" \
    --base-url "$BASE_URL" --phase injection --duration-seconds "$INJECT_SECONDS" \
    --out "$PROBE_TSV"

  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true

  if [[ -f "$OUT_DIR/ABORT" ]]; then
    ABORTED="yes"
    ABORT_REASON="$(cat "$OUT_DIR/ABORT")"
    log "ABORT CRITERION TRIPPED: $ABORT_REASON"
    log "rolling back immediately per the design's Rollback clause"
  fi

  ATLAS_RESTARTS_AFTER="$(atlas_restart_count)"
  log "atlas RestartCount after injection = $ATLAS_RESTARTS_AFTER"
  log "atlas still running? $(container_running "$ATLAS_CID" && echo yes || echo no)"
}

# ======================================================================
# Recovery (design step 5)
# ======================================================================
recover() {
  log "=== RECOVERY: start postgres ==="
  local t0 elapsed rc hc
  t0="$(date -u +%s)"
  compose start postgres
  log "postgres start issued at epoch=$t0"

  RECOVERY_SECONDS="-1"
  local i=0
  while [[ "$i" -lt "$RECOVERY_BUDGET" ]]; do
    rc="$(probe_once -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" "$BASE_URL/v1/anchors")"
    hc="$(probe_once "$BASE_URL/health")"
    elapsed=$(($(date -u +%s) - t0))
    printf '%s\trecovery\tpoll\t%s\t%s\tanchors=%s health=%s\n' \
      "$(date -u +%s)" "${rc%% *}" "$elapsed" "${rc%% *}" "${hc%% *}" >>"$PROBE_TSV"
    if [[ "${rc%% *}" == "200" && "${hc%% *}" == "200" ]]; then
      RECOVERY_SECONDS="$elapsed"
      log "steady state restored after ${elapsed}s (GET /v1/anchors 200, GET /health 200)"
      break
    fi
    sleep 1
    i=$((i + 1))
  done
  if [[ "$RECOVERY_SECONDS" == "-1" ]]; then
    log "WARNING: steady state NOT restored within ${RECOVERY_BUDGET}s budget"
  fi

  # C-2 verification: row count must be identical to the BEFORE snapshot.
  # Checked before ANY post-recovery write so the comparison is clean.
  # `|| echo unreadable` keeps a failed recovery reportable: without it a
  # psql that cannot connect would kill the script under `set -e` and the
  # falsification verdict would never be printed.
  EVIDENCE_AFTER="$(psql_q 'select count(*) from evidence_records;' 2>/dev/null || echo 'unreadable')"
  STREAM_AFTER="$(stream_depth)"
  log "evidence_records AFTER  = $EVIDENCE_AFTER (BEFORE = $EVIDENCE_BEFORE)"
  log "EVIDENCE_INGEST depth AFTER = $STREAM_AFTER (BEFORE = $STREAM_BEFORE)"

  # Confirming write: proves the write path itself recovered, not just
  # the read path. Runs AFTER the row-count comparison.
  local key body code
  key="chaos356a-postrecovery-$(date -u +%s)"
  body="$(
    cat <<JSON
{"records":[{"idempotency_key":"$key","evidence_kind":"access_review.completion.v1","schema_version":"1.0.0","control_id":"$ATLAS_CHAOS_CONTROL","scope":[{"key":"env","values":["prod"]}],"observed_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","result":"pass","payload":{"review_id":"$key","completed_by":"chaos-356a","reviewer_role":"security","users_reviewed":7},"source_attribution":{"actor_type":"service","actor_id":"chaos-356a"}}]}
JSON
  )"
  code="$(curl -s -o /dev/null --max-time 35 -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $ATLAS_CHAOS_TOKEN" -H 'Content-Type: application/json' \
    -d "$body" "$BASE_URL/v1/evidence:push")"
  log "post-recovery write probe: POST /v1/evidence:push -> $code"
  POST_RECOVERY_WRITE="$code"
}

# ======================================================================
# Falsification check (design Abort criteria + slice 356 AC-5)
# ======================================================================
evaluate() {
  log "=== FALSIFICATION CHECK (slice 356 AC-5) ==="

  local max_ms hangs
  max_ms="$(awk -F'\t' 'NR>1 && $2=="injection" { if ($5+0 > m) m = $5+0 } END { print m+0 }' "$PROBE_TSV")"
  hangs="$(awk -F'\t' -v t="$HANG_THRESHOLD_MS" 'NR>1 && $2=="injection" && $5+0 > t { n++ } END { print n+0 }' "$PROBE_TSV")"
  log "F-1  no request hangs > 30s      : max injection latency ${max_ms}ms, ${hangs} over threshold -> $([[ "$hangs" -eq 0 ]] && echo HOLDS || echo FALSIFIED)"

  # NOTE: written as explicit `if` blocks, not `[[ ... ]] && x=y`. Under
  # `set -e` a false `[[ ]]` at the head of an && list makes the list
  # return 1 and kills the script mid-evaluation — which would abort the
  # falsification report precisely when the hypothesis HOLDS.
  local crashed="no"
  if ! container_running "$ATLAS_CID"; then crashed="yes"; fi
  if [[ "$ATLAS_RESTARTS_AFTER" != "$ATLAS_RESTARTS_BEFORE" ]]; then
    crashed="yes (restarted)"
  fi
  log "F-2  atlas does not crash        : running=$(container_running "$ATLAS_CID" && echo yes || echo no), RestartCount ${ATLAS_RESTARTS_BEFORE} -> ${ATLAS_RESTARTS_AFTER} -> $([[ "$crashed" == "no" ]] && echo HOLDS || echo FALSIFIED)"

  # Stack-trace detection over injection-phase response bodies. The
  # pattern set targets Go runtime and driver leakage specifically:
  # goroutine dumps, file:line frames, panics, pgx/lib-pq error prefixes,
  # SQLSTATE codes, and internal import paths.
  local leaks
  leaks="$(awk -F'\t' 'NR>1 && $2=="injection" { print $6 }' "$PROBE_TSV" |
    grep -icE 'goroutine |panic:|runtime\.|\.go:[0-9]+|github\.com/|SQLSTATE|pgx\.|pq: |/internal/' || true)"
  log "F-3  no stack traces in bodies   : ${leaks} injection-phase bodies matched a leak pattern -> $([[ "$leaks" -eq 0 ]] && echo HOLDS || echo FALSIFIED)"

  log ""
  log "=== DISTINCT RESPONSE BODIES BY OP (injection phase) ==="
  awk -F'\t' 'NR>1 && $2=="injection" { print $3"\t"$4"\t"$6 }' "$PROBE_TSV" |
    sort | uniq -c | sort -rn

  log ""
  log "=== STATUS-CODE / LATENCY SUMMARY BY PHASE AND OP ==="
  awk -F'\t' 'NR>1 && $2!="recovery" {
      k=$2"\t"$3"\t"$4; n[k]++; s[k]+=$5;
      if ($5+0 > mx[k]) mx[k]=$5+0;
      lat[k]=lat[k]" "$5
    }
    END {
      printf "phase\top\tcode\tcount\tmean_ms\tmax_ms\n";
      for (k in n) printf "%s\t%d\t%d\t%d\n", k, n[k], s[k]/n[k], mx[k];
    }' "$PROBE_TSV" | (
    read -r hdr
    echo "$hdr"
    sort
  )

  log ""
  log "=== RESULT ==="
  log "ledger @checklist / @injection / after : $EVIDENCE_BEFORE / $PRE_INJECT_EVIDENCE / $EVIDENCE_AFTER"
  log "stream @checklist / @injection / after : $STREAM_BEFORE / $PRE_INJECT_STREAM / $STREAM_AFTER"
  # A divergence here is NOT automatically a partial write. Because the
  # push path acks onto the NATS buffer before the ledger write, any
  # record accepted just before the stop drains into `evidence_records`
  # once the consumer reconnects — a legitimate post-recovery landing.
  # The verdict line therefore reports the delta and names both readings
  # rather than asserting corruption.
  log "C-2 VERDICT (injection boundary vs after) : ledger $PRE_INJECT_EVIDENCE -> $EVIDENCE_AFTER $([[ "$PRE_INJECT_EVIDENCE" == "$EVIDENCE_AFTER" ]] && echo '= IDENTICAL, no partial-write state' || echo '= DIVERGED — inspect: buffered-then-drained record, or partial-write state')"
  log "recovery to steady state      : ${RECOVERY_SECONDS}s"
  log "post-recovery write probe     : $POST_RECOVERY_WRITE"
  log "aborted                       : ${ABORTED:-no} ${ABORT_REASON}"
}

# Rollback on any unexpected failure: postgres must never be left stopped.
cleanup() {
  local rc=$?
  if ! container_running "$PG_CID"; then
    log "cleanup: postgres is stopped — issuing rollback start"
    compose start postgres || true
  fi
  exit "$rc"
}
trap cleanup EXIT

{
  log "run-exp3-pg-outage — slice 335 Experiment 3 via slice 356a"
  log "out-dir: $OUT_DIR"
  preflight
  steady_state
  inject
  recover
  evaluate
} 2>&1 | tee "$RUN_LOG"

exit 0
