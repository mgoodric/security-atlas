#!/usr/bin/env bash
#
# run-exp5-atlas-restart.sh — orchestrator for slice 335 Experiment 5,
# "atlas container restart mid-evidence-push". Executed by slice 356b.
#
# Runs the design's Method against the LOCAL docker-compose stack:
#
#   1. docker-compose up; SDK client pushing 1/s        (design step 1)
#   2. after 10 seconds, `docker compose restart atlas` (design step 2)
#   3. the client observes transient errors             (design step 3)
#   4. push continues through second 60                 (design step 4)
#   5. count evidence_records: must equal 60            (design step 5)
#
# plus a RECOVERY stage, which the design's Method leaves implicit because
# its Rollback field says "none needed — restart was the injection AND its
# own recovery". That claim is itself measurable, so the recovery stage
# measures it rather than assuming it: see run_recovery() below.
#
# The design is the contract. This script does not reinterpret it: the
# 1/s rate, the 60-second window, the t=10s injection point, the
# `restart atlas` mechanism, and the exact-60 ledger assertion are read
# straight out of docs/audits/335-chaos-experiment-design.md §Experiment 5.
#
# TWO ARMS. The design's hypothesis has two conjuncts — the SDK re-sends,
# AND the idempotency key prevents duplicates. `pkg/sdk-go`.Client.Push
# issues exactly one RPC with no retry and no backoff, so a single-arm run
# can only ever report on the first conjunct and would leave the second
# untested. The run therefore executes the design's Method twice:
#
#   arm A  --retry-mode none    the SDK exactly as shipped
#   arm B  --retry-mode design  the design's "1s, 2s, 4s, 8s with jitter"
#                               applied by the caller around that Push
#
# Arm A answers "does evidence survive a restart today?". Arm B answers
# "if the retry the design assumes existed, would the ledger dedup it?".
# Neither substitutes for the other and both verdicts are reported.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 356
# P0-1) — LOAD-BEARING. Every action is scoped to one named local
# docker-compose project. The script refuses to run unless the gRPC
# endpoint host is a loopback literal and the atlas container it is about
# to restart belongs to the compose project rooted at the compose file
# passed in. It therefore cannot restart a container belonging to
# atlas-edge, a hosted deployment, or any unrelated stack.
#
# ABORT CRITERIA (design, verbatim): the SDK client crashes on a transient
# error; OR the ledger row count exceeds 60 (duplicate writes). Neither
# admits a mid-run rollback — the restart IS its own recovery, per the
# design's Rollback field — so both are evaluated as falsifications in the
# verdict rather than as mid-run aborts. A crashed probe is detected by
# its non-zero exit and reported as an abort.
#
# PRE-EXECUTION CHECKLIST (design, verbatim):
#   [ ] Confirm SDK client uses idempotency keys (slice 003 standard).
#       If not, the duplicate-write expectation changes.
#   [ ] Use a fresh tenant scope so prior runs don't pollute the count.
# Both are executed by preflight() below, against the running stack, and
# echoed into the run log so the decisions log signs them off against real
# output rather than against an assertion.
#
# Usage:
#   scripts/chaos/run-exp5-atlas-restart.sh --out-dir /tmp/exp5 \
#     [--atlas-cli ./atlas-cli] [--run-tag r1]
#
# Exit: 0 ran to completion (falsified or not — read the summary),
#       1 refused (guard tripped) or aborted before injection,
#       2 environment error.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker/docker-compose.yml"
ENV_FILE="$REPO_ROOT/deploy/docker/.env"
GRPC_ENDPOINT="127.0.0.1:50051"
OUT_DIR=""
ATLAS_CLI=""
RUN_TAG="r1"
# Design constants. Do not tune to make a run look better.
WINDOW_SECONDS=60
INJECT_AT_SECOND=10
EXPECTED_ROWS=60
# Post-window settle. The ingest path acks at NATS stream-commit, so the
# ledger legitimately keeps growing after the last push returns; counting
# before the stream drains would report a false loss.
DRAIN_STABLE_SECONDS=8
DRAIN_MAX_SECONDS=120
# How long the recovery stage waits for a PRE-restart credential to start
# working again before recording it as unrecovered. Generous relative to the
# ~1s restart so "never" means never, not "not yet".
RECOVERY_DEADLINE_SECONDS=90

die() {
  echo "run-exp5: $1" >&2
  exit "${2:-2}"
}

log() { echo "[$(date -u +%H:%M:%S)] $*" | tee -a "${RUN_LOG:-/dev/null}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --out-dir)
    OUT_DIR="${2:-}"
    shift 2
    ;;
  --atlas-cli)
    ATLAS_CLI="${2:-}"
    shift 2
    ;;
  --run-tag)
    RUN_TAG="${2:-}"
    shift 2
    ;;
  --endpoint)
    GRPC_ENDPOINT="${2:-}"
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
  *) die "unknown argument: $1" ;;
  esac
done

[[ -n "$OUT_DIR" ]] || die "--out-dir is required"
[[ "$RUN_TAG" =~ ^[A-Za-z0-9_-]+$ ]] || die "--run-tag must be [A-Za-z0-9_-]+"
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ -f "$ENV_FILE" ]] || die "env file not found: $ENV_FILE"
command -v docker >/dev/null 2>&1 || die "docker not on PATH"

PROBE="$REPO_ROOT/scripts/chaos/evidence-push-probe.sh"
FIXTURE="$REPO_ROOT/scripts/chaos/ensure-chaos-tenant.sh"
[[ -x "$PROBE" ]] || die "probe not executable: $PROBE"
[[ -x "$FIXTURE" ]] || die "fixture helper not executable: $FIXTURE"

# --- scope guard: loopback gRPC endpoint -------------------------------
_host="${GRPC_ENDPOINT%:*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "run-exp5: REFUSED — endpoint host '$_host' is not loopback." >&2
  echo "  Slice 335 restricts every experiment to the local docker-compose" >&2
  echo "  stack (P0-335-2 / slice 356 P0-1). Refusing to proceed." >&2
  exit 1
  ;;
esac

mkdir -p "$OUT_DIR"
RUN_LOG="$OUT_DIR/run.log"
: >"$RUN_LOG"

compose() { docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"; }

PG_CID="$(compose ps -q postgres || true)"
ATLAS_CID="$(compose ps -q atlas || true)"
[[ -n "$PG_CID" ]] || die "no postgres container in the compose project at $COMPOSE_FILE"
[[ -n "$ATLAS_CID" ]] || die "no atlas container in the compose project at $COMPOSE_FILE"

psql_q() { docker exec "$PG_CID" psql -U postgres -d security_atlas -Atc "$1"; }

# Build the SDK-backed CLI if the caller did not supply one. It is the
# push client under test, so it is built from THIS worktree, not pulled.
if [[ -z "$ATLAS_CLI" ]]; then
  ATLAS_CLI="$OUT_DIR/atlas-cli"
  log "building atlas-cli (the SDK push client under test) from $REPO_ROOT"
  (cd "$REPO_ROOT" && go build -o "$ATLAS_CLI" ./cmd/atlas-cli) || die "go build ./cmd/atlas-cli failed"
fi
[[ -x "$ATLAS_CLI" ]] || die "atlas-cli not executable: $ATLAS_CLI"

# Tenant ids are derived from the run tag so a rerun with a new tag gets a
# genuinely fresh scope and a rerun with the same tag fails loudly on the
# fixture helper's non-empty-ledger check rather than silently mixing runs.
tag_hex="$(printf '%s' "$RUN_TAG" | shasum | cut -c1-8)"
tenant_for() { printf 'dddddddd-%s-4000-8000-%012d' "${tag_hex:0:4}" "$1"; }
control_for() { printf 'cccccccc-%s-4000-8000-%012d' "${tag_hex:0:4}" "$1"; }

# ---------------------------------------------------------------------
# summarize <tsv> — one line of aggregate metrics for a probe run.
#
# Percentiles are taken via `sort -n` rather than awk's asort(), which is
# a gawk extension the BSD awk on this platform does not have. Written as
# gawk it would fail at run time, on the summary line, after the injection
# had already happened and the window could not be replayed.
# ---------------------------------------------------------------------
summarize() {
  local tsv="$1"
  local attempts ok err nk nr codes p50 p95 pmax
  attempts="$(wc -l <"$tsv" | tr -d ' ')"
  if [[ "$attempts" == "0" ]]; then
    printf 'attempts=0 (empty probe output)\n'
    return
  fi
  ok="$(awk -F'\t' '$6=="ok"' "$tsv" | wc -l | tr -d ' ')"
  err=$((attempts - ok))
  nk="$(awk -F'\t' '{print $4}' "$tsv" | sort -u | wc -l | tr -d ' ')"
  nr="$(awk -F'\t' '$6=="ok" && $9!="-" {print $9}' "$tsv" | sort -u | wc -l | tr -d ' ')"
  codes="$(awk -F'\t' '$6!="ok" {print $7}' "$tsv" | sort | uniq -c | awk '{printf "%s=%s ", $2, $1}')"
  read -r p50 p95 pmax <<<"$(awk -F'\t' '{print $8}' "$tsv" | sort -n |
    awk '{v[NR]=$1} END{i50=int(NR*0.5); if(i50<1)i50=1; i95=int(NR*0.95); if(i95<1)i95=1; print v[i50], v[i95], v[NR]}')"
  printf 'attempts=%s ok=%s err=%s distinct_keys=%s distinct_record_ids=%s wall_ms_p50=%s wall_ms_p95=%s wall_ms_max=%s err_codes=[%s]\n' \
    "$attempts" "$ok" "$err" "$nk" "$nr" "$p50" "$p95" "$pmax" "$codes"
}

# ---------------------------------------------------------------------
# drain_and_count <tenant> — wait for the ledger to stop moving, then
# report the settled row count and how long the drain took.
# ---------------------------------------------------------------------
drain_and_count() {
  local tenant="$1" last="" cur stable=0 waited=0
  while [[ $waited -lt $DRAIN_MAX_SECONDS ]]; do
    cur="$(psql_q "select count(*) from evidence_records where tenant_id = '$tenant';")"
    if [[ "$cur" == "$last" ]]; then
      stable=$((stable + 1))
      [[ $stable -ge $DRAIN_STABLE_SECONDS ]] && break
    else
      stable=0
      last="$cur"
    fi
    sleep 1
    waited=$((waited + 1))
  done
  printf '%s %s' "$cur" "$((waited - stable + 1))"
}

# ---------------------------------------------------------------------
# preflight — the design's pre-execution checklist, executed.
# ---------------------------------------------------------------------
CHECKLIST="$OUT_DIR/preflight-checklist.txt"
preflight() {
  : >"$CHECKLIST"
  local ck_tenant ck_control ck_bearer fail=0
  ck_tenant="$(tenant_for 900)"
  ck_control="$(control_for 900)"
  ck_bearer="$OUT_DIR/bearer.preflight"

  log "preflight: provisioning the checklist tenant (kept separate so its pushes never enter an arm's count)"
  "$FIXTURE" --tenant "$ck_tenant" --control "$ck_control" --label "preflight checklist" \
    --bearer-out "$ck_bearer" --atlas-cli "$ATLAS_CLI" \
    --endpoint "$GRPC_ENDPOINT" --compose-file "$COMPOSE_FILE" --env-file "$ENV_FILE" >>"$RUN_LOG" 2>&1 ||
    die "preflight: could not provision the checklist tenant"

  # --- S-1: every compose service is up, atlas is healthy -------------
  local svc_total svc_running atlas_health
  svc_total="$(compose ps --services | wc -l | tr -d ' ')"
  svc_running="$(compose ps --services --filter status=running | wc -l | tr -d ' ')"
  atlas_health="$(docker inspect "$ATLAS_CID" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')"
  if [[ "$svc_total" == "$svc_running" && "$atlas_health" == "healthy" ]]; then
    echo "[PASS] S-1 stack up: $svc_running/$svc_total services running, atlas health=$atlas_health" >>"$CHECKLIST"
  else
    echo "[FAIL] S-1 stack up: $svc_running/$svc_total services running, atlas health=$atlas_health" >>"$CHECKLIST"
    fail=1
  fi

  # --- C-1a: the SDK surface REQUIRES an idempotency key --------------
  local noidem
  set +e
  noidem="$(SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$(cat "$ck_bearer")" \
    "$ATLAS_CLI" evidence push --insecure --kind access_review.completion.v1 \
    --control "$ck_control" --scope '{"env":"prod"}' \
    --observed-at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --result pass \
    --payload '{"review_id":"noidem","completed_by":"chaos-356b","reviewer_role":"security","users_reviewed":1}' \
    --actor-type service --actor-id chaos-356b 2>&1)"
  set -e
  if printf '%s' "$noidem" | grep -q -- '--idempotency-key'; then
    echo "[PASS] C-1a SDK client requires an idempotency key: push without it is refused (\"${noidem//$'\n'/ }\")" >>"$CHECKLIST"
  else
    echo "[FAIL] C-1a push without --idempotency-key was NOT refused: ${noidem//$'\n'/ }" >>"$CHECKLIST"
    fail=1
  fi

  # --- C-1b: dedup on replay, and the record actually LANDS -----------
  # The landing half is the slice 356a D2 guard: the ingest path acks at
  # NATS stream-commit, so a record that can never be inserted still
  # returns a receipt. A checklist that only reads receipts would sign off
  # a completely non-functional write path.
  local idem observed payload r1 r2 rid1 rid2 before after
  idem="preflight-${RUN_TAG}-$(date +%s)"
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  payload='{"review_id":"preflight","completed_by":"chaos-356b","reviewer_role":"security","users_reviewed":4}'
  before="$(psql_q "select count(*) from evidence_records where tenant_id = '$ck_tenant';")"
  r1="$(SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$(cat "$ck_bearer")" \
    "$ATLAS_CLI" evidence push --insecure --kind access_review.completion.v1 --schema-version 1.0.0 \
    --control "$ck_control" --scope '{"env":"prod"}' --observed-at "$observed" --result pass \
    --payload "$payload" --idempotency-key "$idem" --actor-type service --actor-id chaos-356b 2>&1)" ||
    die "preflight: the checklist push failed outright: $r1"
  read -r after _ <<<"$(drain_and_count "$ck_tenant")"
  if [[ "$after" -gt "$before" ]]; then
    echo "[PASS] C-1b(i) the checklist push LANDED in the ledger: $before -> $after rows" >>"$CHECKLIST"
  else
    echo "[FAIL] C-1b(i) the checklist push did NOT land: ledger stayed at $before. A receipt without a row means the consumer is rejecting the insert (composite FK on (tenant_id, control_id) is the usual cause — see slice 356a D2). Refusing to inject into a stack whose write path does not work." >>"$CHECKLIST"
    fail=1
  fi

  r2="$(SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$(cat "$ck_bearer")" \
    "$ATLAS_CLI" evidence push --insecure --kind access_review.completion.v1 --schema-version 1.0.0 \
    --control "$ck_control" --scope '{"env":"prod"}' --observed-at "$observed" --result pass \
    --payload "$payload" --idempotency-key "$idem" --actor-type service --actor-id chaos-356b 2>&1)" ||
    die "preflight: the replay push errored, which the idempotency contract forbids: $r2"
  rid1="$(printf '%s' "$r1" | sed -n 's/.*"recordId"[^"]*"\([^"]*\)".*/\1/p' | head -1)"
  rid2="$(printf '%s' "$r2" | sed -n 's/.*"recordId"[^"]*"\([^"]*\)".*/\1/p' | head -1)"
  read -r before _ <<<"$(drain_and_count "$ck_tenant")"
  if [[ "$rid1" == "$rid2" && "$before" == "$after" ]]; then
    echo "[PASS] C-1b(ii) replay of the same key + same content deduplicates: same record_id ($rid1), ledger unchanged at $after rows" >>"$CHECKLIST"
  else
    echo "[FAIL] C-1b(ii) replay did not deduplicate: record_id $rid1 vs $rid2, ledger $after -> $before" >>"$CHECKLIST"
    fail=1
  fi

  # --- C-1c: same key + different content is refused ------------------
  local r3
  set +e
  r3="$(SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$(cat "$ck_bearer")" \
    "$ATLAS_CLI" evidence push --insecure --kind access_review.completion.v1 --schema-version 1.0.0 \
    --control "$ck_control" --scope '{"env":"prod"}' --observed-at "$observed" --result pass \
    --payload '{"review_id":"preflight","completed_by":"chaos-356b","reviewer_role":"security","users_reviewed":999}' \
    --idempotency-key "$idem" --actor-type service --actor-id chaos-356b 2>&1)"
  set -e
  if printf '%s' "$r3" | grep -q 'AlreadyExists'; then
    echo "[PASS] C-1c same key + different content is refused with AlreadyExists" >>"$CHECKLIST"
  else
    echo "[FAIL] C-1c same key + different content was not refused: ${r3//$'\n'/ }" >>"$CHECKLIST"
    fail=1
  fi

  # --- C-1d: the ledger enforces uniqueness in the schema, not only in
  #           application code ---------------------------------------
  local idx
  idx="$(psql_q "select indexdef from pg_indexes where tablename='evidence_records' and indexname='evidence_records_tenant_idem_uniq';")"
  if [[ -n "$idx" ]]; then
    echo "[PASS] C-1d ledger-level uniqueness index present: $idx" >>"$CHECKLIST"
  else
    echo "[FAIL] C-1d no unique index on (tenant_id, idempotency_key) — the duplicate-write expectation changes" >>"$CHECKLIST"
    fail=1
  fi

  # --- C-2: fresh tenant scope for every arm --------------------------
  # Slot 4 is the recovery stage's tenant and is swept here for a reason:
  # the fixture helper refuses a tenant that already holds ledger rows, and
  # the recovery stage runs LAST. A stale slot 4 caught only at its own
  # provisioning step would abort the run AFTER all three injections, with
  # the recovery measurement — design step 6 — unrecoverably missing from
  # a window that cannot be replayed. Catching it here fails before the
  # first injection instead.
  local t rows all_fresh=1 detail=""
  for t in "$(tenant_for 1)" "$(tenant_for 2)" "$(tenant_for 3)" "$(tenant_for 4)"; do
    rows="$(psql_q "select count(*) from evidence_records where tenant_id = '$t';")"
    detail="$detail $t=$rows"
    [[ "$rows" == "0" ]] || all_fresh=0
  done
  if [[ "$all_fresh" == "1" ]]; then
    echo "[PASS] C-2 fresh tenant scope: every arm tenant holds 0 evidence_records rows —$detail" >>"$CHECKLIST"
  else
    echo "[FAIL] C-2 an arm tenant already holds rows —$detail. Rerun with a new --run-tag." >>"$CHECKLIST"
    fail=1
  fi

  # --- H-1: harness floor, so wall_ms is not mistaken for ack latency --
  local floor
  TIMEFORMAT='%3R'
  floor="$( { time "$ATLAS_CLI" version >/dev/null 2>&1; } 2>&1 )"
  echo "[INFO] H-1 harness floor: a no-RPC \`atlas-cli version\` costs ${floor}s through the same measurement construct. Every wall_ms below includes that process-start cost and is NOT the platform's ack latency." >>"$CHECKLIST"

  tee -a "$RUN_LOG" <"$CHECKLIST"
  if [[ "$fail" != "0" ]]; then
    log "preflight: CHECKLIST NOT SATISFIED — refusing to inject. Slice 356 P0: do not skip the checklist to get to the injection faster."
    exit 1
  fi
  log "preflight: checklist satisfied"
}

# ---------------------------------------------------------------------
# run_arm <slot> <label> <retry-mode> <inject:yes|no>
# ---------------------------------------------------------------------
run_arm() {
  local slot="$1" label="$2" retry_mode="$3" inject="$4"
  local tenant control bearer tsv rows drain probe_rc
  tenant="$(tenant_for "$slot")"
  control="$(control_for "$slot")"
  bearer="$OUT_DIR/bearer.$label"
  tsv="$OUT_DIR/$label.tsv"

  log "=== $label: retry-mode=$retry_mode inject=$inject tenant=$tenant ==="
  "$FIXTURE" --tenant "$tenant" --control "$control" --label "$label" \
    --bearer-out "$bearer" --atlas-cli "$ATLAS_CLI" \
    --endpoint "$GRPC_ENDPOINT" --compose-file "$COMPOSE_FILE" --env-file "$ENV_FILE" >>"$RUN_LOG" 2>&1 ||
    die "$label: could not provision the arm tenant"

  local restart_started restart_returned
  set +e
  "$PROBE" --atlas-cli "$ATLAS_CLI" --bearer-file "$bearer" --control "$control" \
    --run-id "$label" --phase "$label" --duration-seconds "$WINDOW_SECONDS" \
    --retry-mode "$retry_mode" --endpoint "$GRPC_ENDPOINT" --out "$tsv" &
  local probe_pid=$!
  set -e

  if [[ "$inject" == "yes" ]]; then
    sleep "$INJECT_AT_SECOND"
    restart_started="$(date +%s)"
    log "$label: INJECT — docker compose restart atlas (t=+${INJECT_AT_SECOND}s)"
    compose restart atlas >>"$RUN_LOG" 2>&1 || log "$label: WARNING restart returned non-zero"
    restart_returned="$(date +%s)"
    log "$label: restart command returned after $((restart_returned - restart_started))s"
    echo "$restart_started $restart_returned" >"$OUT_DIR/$label.restart"
  fi

  set +e
  wait "$probe_pid"
  probe_rc=$?
  set -e
  if [[ "$probe_rc" != "0" ]]; then
    log "$label: ABORT CRITERION — the SDK push client exited non-zero ($probe_rc). The design treats a client crash on a transient error as a falsification of the SDK contract."
    echo "probe_exit=$probe_rc" >>"$OUT_DIR/$label.status"
  else
    echo "probe_exit=0" >>"$OUT_DIR/$label.status"
  fi

  read -r rows drain <<<"$(drain_and_count "$tenant")"
  log "$label: ledger settled at $rows rows after a ${drain}s drain (expected $EXPECTED_ROWS)"
  {
    echo "tenant=$tenant"
    echo "retry_mode=$retry_mode"
    echo "injected=$inject"
    echo "ledger_rows=$rows"
    echo "drain_seconds=$drain"
    summarize "$tsv"
    echo "ledger_distinct_record_ids=$(psql_q "select count(distinct id) from evidence_records where tenant_id = '$tenant';")"
    echo "ledger_distinct_idem_keys=$(psql_q "select count(distinct idempotency_key) from evidence_records where tenant_id = '$tenant';")"
    echo "ledger_distinct_hashes=$(psql_q "select count(distinct hash) from evidence_records where tenant_id = '$tenant';")"
  } >>"$OUT_DIR/$label.status"
  log "$label: $(summarize "$tsv")"
}

# ---------------------------------------------------------------------
# push_code <bearer-file> <control> <idem> — one push; echoes `ok` or the
# gRPC status code. Used by the recovery stage's poll loop, where the CODE
# is the signal and the receipt is not needed.
# ---------------------------------------------------------------------
push_code() {
  local bearer_file="$1" control="$2" idem="$3" out rc
  set +e
  out="$(SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$(cat "$bearer_file")" \
    "$ATLAS_CLI" evidence push --insecure --kind access_review.completion.v1 --schema-version 1.0.0 \
    --control "$control" --scope '{"env":"prod"}' \
    --observed-at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --result pass \
    --payload "{\"review_id\":\"$idem\",\"completed_by\":\"chaos-356b\",\"reviewer_role\":\"security\",\"users_reviewed\":1}" \
    --idempotency-key "$idem" --actor-type service --actor-id chaos-356b 2>&1)"
  rc=$?
  set -e
  if [[ "$rc" == "0" ]]; then
    printf 'ok'
  else
    printf '%s' "$(printf '%s' "$out" | sed -n 's/.*code = \([A-Za-z]*\).*/\1/p' | head -1)"
  fi
}

# ---------------------------------------------------------------------
# issue_bearer <tenant> <out-file> — re-issue a tenant-scoped credential
# with the bootstrap admin token. This is the OPERATOR REMEDIATION step,
# isolated here so the recovery stage can time it. Echoes nothing; writes
# the bearer to <out-file> at 0600. Returns non-zero if the admin surface
# is not answering yet.
# ---------------------------------------------------------------------
issue_bearer() {
  local tenant="$1" out="$2" admin issue_json bearer
  admin="$(
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
    printf '%s' "${ATLAS_BOOTSTRAP_TOKEN:-}"
  )"
  [[ -n "$admin" ]] || die "ATLAS_BOOTSTRAP_TOKEN not set in $ENV_FILE"
  set +e
  issue_json="$(SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$admin" \
    "$ATLAS_CLI" credentials issue --insecure --tenant "$tenant" --ttl 0 2>&1)"
  local rc=$?
  set -e
  [[ "$rc" == "0" ]] || return 1
  bearer="$(printf '%s' "$issue_json" | sed -n 's/.*"bearerToken"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  [[ -n "$bearer" ]] || return 1
  umask 077
  printf '%s' "$bearer" >"$out"
  chmod 600 "$out"
}

# ---------------------------------------------------------------------
# run_recovery — design step 6: recover, and measure recovery time to
# steady state.
#
# The design's Rollback field asserts "none needed — restart was the
# injection AND its own recovery". This stage MEASURES that assertion
# instead of inheriting it, and separates three distinct clocks that the
# single phrase "recovery time" conflates:
#
#   t_surface   when the gRPC ingest surface answers at all again. Detected
#               as the first poll whose status code is NOT Unavailable: a
#               dialable-but-rejecting surface is a live process.
#   t_old       when a credential issued BEFORE the restart pushes
#               successfully again. If this never happens inside
#               RECOVERY_DEADLINE_SECONDS it is recorded as unrecovered,
#               which is a finding and not a timeout bug.
#   t_reissue   when the push path works again AFTER the operator re-issues
#               the credential. This is recovery-with-intervention, and it
#               is only meaningful if t_old did not recover on its own.
#
# All three are measured from the instant the restart command is issued.
# ---------------------------------------------------------------------
run_recovery() {
  local label="recovery" tenant control bearer_old bearer_new
  tenant="$(tenant_for 4)"
  control="$(control_for 4)"
  bearer_old="$OUT_DIR/bearer.recovery_pre"
  bearer_new="$OUT_DIR/bearer.recovery_post"

  log "=== $label: measuring recovery to steady state after a restart ==="
  "$FIXTURE" --tenant "$tenant" --control "$control" --label "$label" \
    --bearer-out "$bearer_old" --atlas-cli "$ATLAS_CLI" \
    --endpoint "$GRPC_ENDPOINT" --compose-file "$COMPOSE_FILE" --env-file "$ENV_FILE" >>"$RUN_LOG" 2>&1 ||
    die "$label: could not provision the recovery tenant"

  # Establish that this credential works BEFORE the restart. Without this,
  # a post-restart rejection could not be attributed to the restart.
  local pre
  pre="$(push_code "$bearer_old" "$control" "recovery-pre-$RUN_TAG")"
  [[ "$pre" == "ok" ]] || die "$label: the pre-restart push failed ($pre) — cannot attribute anything to the injection"
  log "$label: pre-restart push with the pre-restart credential: ok"

  local t0 elapsed code t_surface="" t_old="" t_reissue=""
  t0="$(date +%s)"
  log "$label: INJECT — docker compose restart atlas"
  compose restart atlas >>"$RUN_LOG" 2>&1 || log "$label: WARNING restart returned non-zero"

  while :; do
    elapsed=$(($(date +%s) - t0))
    [[ $elapsed -ge $RECOVERY_DEADLINE_SECONDS ]] && break
    code="$(push_code "$bearer_old" "$control" "recovery-old-$RUN_TAG-$elapsed")"
    if [[ -z "$t_surface" && "$code" != "Unavailable" ]]; then
      t_surface="$elapsed"
      log "$label: gRPC surface answering again at t+${t_surface}s (first non-Unavailable code: $code)"
    fi
    if [[ "$code" == "ok" ]]; then
      t_old="$elapsed"
      log "$label: the PRE-restart credential works again at t+${t_old}s"
      break
    fi
    sleep 1
  done
  [[ -n "$t_old" ]] || log "$label: the PRE-restart credential never worked again within ${RECOVERY_DEADLINE_SECONDS}s"

  # Operator remediation: re-issue the credential, then re-establish steady
  # state on the new one.
  if [[ -z "$t_old" ]]; then
    local deadline=$((RECOVERY_DEADLINE_SECONDS + 60))
    while :; do
      elapsed=$(($(date +%s) - t0))
      [[ $elapsed -ge $deadline ]] && break
      if issue_bearer "$tenant" "$bearer_new"; then
        code="$(push_code "$bearer_new" "$control" "recovery-new-$RUN_TAG-$elapsed")"
        if [[ "$code" == "ok" ]]; then
          t_reissue=$(($(date +%s) - t0))
          log "$label: push path recovered at t+${t_reissue}s, but ONLY after re-issuing the credential"
          break
        fi
      fi
      sleep 1
    done
  fi

  {
    echo "tenant=$tenant"
    echo "surface_recovery_seconds=${t_surface:-never}"
    echo "pre_restart_credential_recovery_seconds=${t_old:-never_within_${RECOVERY_DEADLINE_SECONDS}s}"
    echo "recovery_seconds_with_reissue=${t_reissue:-n/a}"
    echo "ledger_rows=$(psql_q "select count(*) from evidence_records where tenant_id = '$tenant';")"
  } >>"$OUT_DIR/$label.status"
}

# ---------------------------------------------------------------------
# verdict — evaluate the design's falsification checks, per arm, from the
# artifacts. Stated as checks that CAN fail: a check that cannot fail is
# not a falsification check.
# ---------------------------------------------------------------------
field() { sed -n "s/^$2=//p" "$OUT_DIR/$1.status" | head -1; }

verdict() {
  local vf="$OUT_DIR/verdict.txt"
  : >"$vf"
  local label rows rid keys mode exit_code

  for label in steady_state injection_armA injection_armB; do
    rows="$(field "$label" ledger_rows)"
    rid="$(field "$label" ledger_distinct_record_ids)"
    keys="$(field "$label" ledger_distinct_idem_keys)"
    mode="$(field "$label" retry_mode)"
    exit_code="$(field "$label" probe_exit)"

    echo "--- $label (retry_mode=$mode)" >>"$vf"

    if [[ "$rows" == "$EXPECTED_ROWS" ]]; then
      echo "  [HOLDS]     no evidence lost, no duplicates: ledger = $rows, expected $EXPECTED_ROWS" >>"$vf"
    elif [[ "$rows" -lt "$EXPECTED_ROWS" ]]; then
      echo "  [FALSIFIED] EVIDENCE LOST: ledger = $rows, expected $EXPECTED_ROWS ($((EXPECTED_ROWS - rows)) records never reached the ledger)" >>"$vf"
    else
      echo "  [FALSIFIED] DUPLICATE WRITES: ledger = $rows, expected $EXPECTED_ROWS. The idempotency-key claim does not hold." >>"$vf"
    fi

    if [[ "$rid" == "$rows" ]]; then
      echo "  [HOLDS]     every ledger record_id is unique ($rid distinct over $rows rows)" >>"$vf"
    else
      echo "  [FALSIFIED] record_ids are NOT unique: $rid distinct over $rows rows" >>"$vf"
    fi

    if [[ "$keys" == "$rows" ]]; then
      echo "  [HOLDS]     one ledger row per idempotency key ($keys distinct keys over $rows rows)" >>"$vf"
    else
      echo "  [FALSIFIED] idempotency keys collapsed or multiplied: $keys distinct keys over $rows rows" >>"$vf"
    fi

    if [[ "$exit_code" == "0" ]]; then
      echo "  [HOLDS]     the SDK push client did not crash on transient errors (exit 0)" >>"$vf"
    else
      echo "  [FALSIFIED] the SDK push client exited $exit_code — the design's abort criterion" >>"$vf"
    fi
  done

  {
    echo "--- arm-A-specific: the SDK retry conjunct"
    echo "  Arm A runs pkg/sdk-go as shipped. If it lost records that arm B"
    echo "  recovered with the design's own backoff schedule, the difference"
    echo "  is the retry layer the design assumed the SDK already had."
    echo "  arm A ledger=$(field injection_armA ledger_rows)  arm B ledger=$(field injection_armB ledger_rows)  steady state ledger=$(field steady_state ledger_rows)"
  } >>"$vf"

  # Was the dedup conjunct actually exercised? Reporting "idempotency holds"
  # off a run that never re-sent a key would be claiming a verdict the data
  # does not carry.
  #
  # The test is NOT "did a retry fire". A retry that the surface rejects at
  # the auth boundary never reaches Service.Process, so it cannot exercise
  # the ledger's dedup no matter how faithfully the client re-sent it. The
  # run of record proved the distinction matters: two retries fired into the
  # restart window and BOTH came back Unauthenticated, so an
  # attempt-count-only heuristic reported the conjunct as exercised when
  # zero same-key re-sends had reached the ledger.
  #
  # The condition is therefore a retry attempt that the LEDGER answered —
  # `ok` (deduplicated to the original receipt) or `AlreadyExists` (same key,
  # different content). Both are ledger verdicts; every other code is a
  # rejection upstream of it.
  local retried retried_fired
  retried_fired="$(awk -F'\t' '$5>1' "$OUT_DIR/injection_armB.tsv" 2>/dev/null | wc -l | tr -d ' ')"
  retried="$(awk -F'\t' '$5>1 && ($6=="ok" || $7=="AlreadyExists")' "$OUT_DIR/injection_armB.tsv" 2>/dev/null | wc -l | tr -d ' ')"
  {
    echo "--- arm-B-specific: was the idempotency/dedup conjunct exercised?"
    if [[ "${retried:-0}" -gt 0 ]]; then
      echo "  YES — $retried of $retried_fired retry attempts were answered by"
      echo "  the ledger, so a same-key re-send did reach dedup and the claim"
      echo "  is under test by the injection."
    else
      echo "  NO — $retried_fired retry attempts fired in the backoff arm and"
      echo "  none of them reached the ledger. A re-send rejected at the auth"
      echo "  boundary never reaches Service.Process, so it cannot exercise"
      echo "  dedup. The dedup conjunct is therefore NOT exercised by this"
      echo "  injection. It is separately verified out-of-band by preflight"
      echo "  checks C-1b(ii) and C-1c."
    fi
  } >>"$vf"

  # `n/a` and `never` are real outcomes here, so they are rendered as
  # themselves rather than glued into a "t+...s" template that would print
  # the nonsense "t+n/as" and read as a measurement.
  local t_sfc t_reiss
  t_sfc="$(field recovery surface_recovery_seconds)"
  t_reiss="$(field recovery recovery_seconds_with_reissue)"
  [[ "$t_sfc" =~ ^[0-9]+$ ]] && t_sfc="t+${t_sfc}s"
  [[ "$t_reiss" =~ ^[0-9]+$ ]] && t_reiss="t+${t_reiss}s"
  {
    echo "--- recovery (design step 6; the design's Rollback field claims none is needed)"
    echo "  gRPC surface answering again:              $t_sfc"
    echo "  PRE-restart credential working again:      $(field recovery pre_restart_credential_recovery_seconds)"
    echo "  push path working again after a re-issue:  $t_reiss"
    if [[ "$(field recovery pre_restart_credential_recovery_seconds)" == never_within_* ]]; then
      echo "  [FALSIFIED] \"the restart is its own recovery\" does NOT hold for"
      echo "  the push path: a credential issued before the restart never"
      echo "  works again. Recovery requires operator re-issuance, so the"
      echo "  recovery time for an unattended push client is unbounded."
    else
      echo "  [HOLDS]     the restart was its own recovery for the push path."
    fi
  } >>"$vf"

  tee -a "$RUN_LOG" <"$vf"
}

main() {
  log "slice 335 Experiment 5 — atlas container restart mid-evidence-push"
  log "compose project rooted at $COMPOSE_FILE; atlas container $ATLAS_CID"
  preflight

  run_arm 1 steady_state none no
  run_arm 2 injection_armA none yes
  run_arm 3 injection_armB design yes
  run_recovery

  log "=== summary ==="
  local f
  for f in "$OUT_DIR"/*.status; do
    echo "--- $(basename "$f" .status)" | tee -a "$RUN_LOG"
    tee -a "$RUN_LOG" <"$f"
  done
  log "=== falsification checks ==="
  verdict
  log "artifacts in $OUT_DIR"
}

main "$@"
