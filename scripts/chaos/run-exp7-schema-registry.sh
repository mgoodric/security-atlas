#!/usr/bin/env bash
#
# run-exp7-schema-registry.sh — orchestrator for slice 335 Experiment 7,
# "schema-registry unavailable". Executed by slice 358.
#
# Runs the design's Method against the LOCAL docker-compose stack:
#
#   1. verify the platform's current schema-registry shape   (design step 1)
#   2. bring-up + push one known-kind record; verify success (design step 2)
#   3. inject: make the registry unavailable                 (design step 3)
#   4. push a known-kind record — should succeed (cached)    (design step 4)
#   5. push an unknown-kind record — should 503, not 400/2xx (design step 5)
#   6. restore; unknown-kind push transitions back to 400    (design step 6)
#
# The design is the contract. This script does not reinterpret the
# hypothesis, the steady state, the abort criteria, or the expected
# outcomes: they are read straight out of
# docs/audits/335-chaos-experiment-design.md §Experiment 7 and evaluated
# against what the stack actually returns.
#
# TWO THINGS THE DESIGN LEAVES TO THE EXECUTOR, both recorded here rather
# than silently chosen:
#
# (1) THE INJECTION MECHANISM. The design's Variable field says "container
#     stop (if schema registry is a separate process per slice 068) OR
#     in-process flag (if embedded)" and its Method step 1 instructs the
#     executor to confirm the current shape first, because "the injection
#     mechanism differs". On this stack the registry is NEITHER: it is an
#     in-process Go service (`internal/api/schemaregistry.Service`) with no
#     availability flag, backed by the `evidence_kind_schemas` table in
#     Postgres and fronted by a process-local hot cache. There is no
#     container to stop and no flag to set.
#
#     What "the registry is unavailable" means for that shape is that its
#     backing store stops answering while the rest of the platform keeps
#     running. The injection is therefore:
#
#         REVOKE SELECT ON evidence_kind_schemas FROM atlas_app;
#
#     — applied inside the compose project's own postgres container. It is
#     surgical (the registry's DB read path is the ONLY thing that breaks;
#     the ledger, NATS, MinIO, and auth are untouched), reversible by the
#     matching GRANT, and confined to the design's stated blast radius
#     ("schema-registry surface only"). Preflight check C-3 verifies
#     mechanically that no package outside the registry reads that table,
#     so the blast-radius claim is checked rather than asserted. Stopping
#     Postgres outright would have been Experiment 3, not this one, and
#     would confound the registry surface with the ledger surface.
#
# (2) THE WINDOW. Experiment 7's Method is a step sequence and names no
#     duration — unlike Experiments 1, 2 and 5, which specify 60-second
#     windows. This run adopts that same 60-second window for each of the
#     three phases (steady state, injected, recovered) rather than inventing
#     a number or firing a single one-shot probe per step. Override with
#     --window-seconds; the decisions log records what was actually used.
#
# THE COLD-MISS PROBE. Slice 358's stated reason for standing alone is that
# it "distinguishes hot-cache vs cold-miss behavior". A known kind and an
# unknown kind cannot draw that distinction on their own: the first never
# touches the registry's store, and the second is invalid regardless of
# whether the registry is up. So each phase also pushes exactly one
# tenant-private kind that IS registered but has NEVER been pushed — the one
# probe class that is both valid and forced through the registry's DB
# lookup. One dedicated cold kind per phase, because the first push of a
# kind hydrates the tenant cache and it stops being cold.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 358
# P0-1) — LOAD-BEARING. Every action is scoped to one named local
# docker-compose project: the HTTP base must be a loopback literal, the
# postgres container the injection runs inside is resolved FROM that
# project, and preflight refuses a project carrying an edge/hosted-looking
# service. The script cannot reach atlas-edge, a hosted instance, or any
# host outside this machine's compose stack.
#
# ABORT CRITERIA (design, verbatim): unknown-kind push returns 2xx
# (falsifies the fail-fast claim) OR known-kind push fails (falsifies the
# hot-cache decoupling). Neither is a reason to stop mid-run here — both are
# observations about the steady, already-shipped behavior of the push API
# rather than an escalating hazard to the stack — so both are evaluated as
# falsifications in the verdict block, and the injection is rolled back on
# EVERY exit path regardless (see the EXIT trap).
#
# PRE-EXECUTION CHECKLIST (design, verbatim):
#   [ ] Identify the schema-registry's current deployment shape
#       (in-process vs containerized) — slice 068 documents the v1 shape;
#       later slices may have changed it.
#   [ ] Choose a known-kind that has been pushed at least once in the
#       current process lifetime (else hot-cache may not have it).
# Both are executed by preflight() below against the RUNNING stack, joined
# by three more checks this shape needs (injection mechanism, fresh tenant
# scope, scope boundary), and echoed into checklist.txt so the decisions log
# signs them off against real output. Injection does not start unless every
# check passes.
#
# Usage:
#   scripts/chaos/run-exp7-schema-registry.sh --out-dir /tmp/exp7 \
#     [--project security-atlas-chaos358] [--http-base http://127.0.0.1:38080] \
#     [--window-seconds 60] [--run-tag r1]
#
# Exit: 0 ran to completion (falsified or not — read the summary),
#       1 refused (guard tripped) or aborted before injection,
#       2 environment error.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker/docker-compose.yml"
ENV_FILE="$REPO_ROOT/deploy/docker/.env"
PROJECT="security-atlas-chaos358"
HTTP_BASE="http://127.0.0.1:38080"
OUT_DIR=""
RUN_TAG="r1"
WINDOW_SECONDS=60
COLD_AT_TICK=5
# Design constant: the known kind must be one the platform ships globally.
KNOWN_KIND="access_review.completion.v1"
KNOWN_SEMVER="1.0.0"
# A kind registered nowhere, global or private.
UNKNOWN_KIND="chaos358.absent_kind.v1"
# Post-window settle. The push API acks at NATS stream-commit, so the ledger
# legitimately keeps growing after the last push returns; counting before the
# stream drains would report a false loss.
DRAIN_STABLE_SECONDS=8
DRAIN_MAX_SECONDS=120
RECOVERY_DEADLINE_SECONDS=60

die() {
  echo "run-exp7: $1" >&2
  exit "${2:-2}"
}

log() { echo "[$(date -u +%H:%M:%S)] $*" | tee -a "${RUN_LOG:-/dev/null}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --out-dir)
    OUT_DIR="${2:-}"
    shift 2
    ;;
  --project)
    PROJECT="${2:-}"
    shift 2
    ;;
  --http-base)
    HTTP_BASE="${2:-}"
    shift 2
    ;;
  --window-seconds)
    WINDOW_SECONDS="${2:-}"
    shift 2
    ;;
  --run-tag)
    RUN_TAG="${2:-}"
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
[[ "$WINDOW_SECONDS" =~ ^[0-9]+$ ]] || die "--window-seconds must be an integer"
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ -f "$ENV_FILE" ]] || die "env file not found: $ENV_FILE"
command -v docker >/dev/null 2>&1 || die "docker not on PATH"
command -v curl >/dev/null 2>&1 || die "curl not on PATH"

PROBE="$REPO_ROOT/scripts/chaos/schema-registry-probe.sh"
[[ -x "$PROBE" ]] || die "probe not executable: $PROBE"

# --- scope guard: loopback HTTP base ------------------------------------
_hostport="${HTTP_BASE#*://}"
_host="${_hostport%%:*}"
_host="${_host%%/*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "run-exp7: REFUSED — HTTP base host '$_host' is not loopback." >&2
  echo "  Slice 335 restricts every experiment to the local docker-compose" >&2
  echo "  stack (P0-335-2 / slice 358 P0-1). Refusing to proceed." >&2
  exit 1
  ;;
esac

mkdir -p "$OUT_DIR"
RUN_LOG="$OUT_DIR/run.log"
CHECKLIST="$OUT_DIR/checklist.txt"
: >"$RUN_LOG"
: >"$CHECKLIST"

RUN_ID="exp7${RUN_TAG}"
# Fixture identifiers carry the run's epoch so a rerun cannot silently reuse
# the previous run's tenant — the C-4 fresh-scope check would refuse a
# tenant that already holds ledger rows, and a per-run tenant keeps each
# run's counts unmixed. Both stay UUID-shaped (digits are valid hex).
RUN_EPOCH="$(date +%s)"
TENANT_ID="35800000-0000-4000-8000-$(printf '%012d' "$RUN_EPOCH")"
CONTROL_ID="35800000-0000-4000-8000-c$(printf '%011d' "$RUN_EPOCH")"
COLD_KIND_STEADY="chaos358.cold_${RUN_ID}_steady.v1"
COLD_KIND_INJECTED="chaos358.cold_${RUN_ID}_injected.v1"
COLD_KIND_RECOVERED="chaos358.cold_${RUN_ID}_recovered.v1"

compose() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

PG_CID=""
psql_q() {
  docker exec "$PG_CID" psql -U postgres -d "${POSTGRES_DB:-security_atlas}" -Atc "$1"
}
psql_do() {
  docker exec "$PG_CID" psql -U postgres -d "${POSTGRES_DB:-security_atlas}" -c "$1"
}

HDR_FILE=""
rollback() {
  # Idempotent, and safe to run when no injection ever happened. This runs
  # on EVERY exit path — success, failure, or interrupt — so the stack is
  # never left with the registry disabled.
  if [[ -n "$PG_CID" ]]; then
    psql_do "GRANT SELECT ON evidence_kind_schemas TO atlas_app;" >/dev/null 2>&1 || true
  fi
}
trap rollback EXIT INT TERM

CHECK_FAILED=0
check() {
  local id="$1" verdict="$2" detail="$3"
  printf '%-6s %-4s %s\n' "$id" "$verdict" "$detail" | tee -a "$CHECKLIST" >>"$RUN_LOG"
  printf '%-6s %-4s %s\n' "$id" "$verdict" "$detail"
  [[ "$verdict" == "PASS" ]] || CHECK_FAILED=1
}

http_code() {
  # http_code <method> <path> [data]
  local method="$1" path="$2" data="${3:-}"
  if [[ -n "$data" ]]; then
    curl -sS -o "$OUT_DIR/.body" -w '%{http_code}' --connect-timeout 3 --max-time 10 \
      -H "@$HDR_FILE" -X "$method" "$HTTP_BASE$path" -d "$data" 2>>"$OUT_DIR/curl.err" || true
  else
    curl -sS -o "$OUT_DIR/.body" -w '%{http_code}' --connect-timeout 3 --max-time 10 \
      -H "@$HDR_FILE" -X "$method" "$HTTP_BASE$path" 2>>"$OUT_DIR/curl.err" || true
  fi
}

# ----------------------------------------------------------------------
# preflight — the design's pre-execution checklist, plus the three checks
# this registry shape needs. Every check writes a PASS/FAIL line to
# checklist.txt. A single FAIL blocks the injection.
# ----------------------------------------------------------------------
preflight() {
  log "preflight: resolving compose project '$PROJECT'"
  PG_CID="$(compose ps -q postgres || true)"
  [[ -n "$PG_CID" ]] || die "no postgres container in compose project '$PROJECT'"
  local atlas_cid
  atlas_cid="$(compose ps -q atlas || true)"
  [[ -n "$atlas_cid" ]] || die "no atlas container in compose project '$PROJECT'"

  # --- C-5: scope boundary -------------------------------------------
  local services edgey
  services="$(compose ps --services --all | sort | tr '\n' ' ')"
  edgey="$(printf '%s' "$services" | tr ' ' '\n' | grep -Ei 'edge|hosted|prod' || true)"
  if [[ -n "$edgey" ]]; then
    check "C-5" "FAIL" "compose project '$PROJECT' carries edge/hosted-looking service(s): $edgey"
  else
    check "C-5" "PASS" "scope: loopback HTTP base $HTTP_BASE; project '$PROJECT' services: $services"
  fi

  # --- C-1: design checklist item 1 — registry deployment shape ------
  # (a) no separate registry container, (b) the registry's HTTP surface is
  # served by the atlas process itself, (c) it is Postgres-backed.
  local registry_svc global_rows
  registry_svc="$(printf '%s' "$services" | tr ' ' '\n' | grep -Ei 'schema|registry' || true)"
  global_rows="$(psql_q "select count(*) from evidence_kind_schemas where tenant_id is null;" 2>/dev/null || echo "ERR")"
  if [[ -n "$registry_svc" ]]; then
    check "C-1" "FAIL" "a separate registry service exists ($registry_svc) — the design's container-stop injection applies; this script's in-process mechanism does not"
  elif [[ "$global_rows" == "ERR" || "$global_rows" == "0" ]]; then
    check "C-1" "FAIL" "evidence_kind_schemas has no global rows (got '$global_rows') — registry not Postgres-backed as expected"
  else
    check "C-1" "PASS" "registry is IN-PROCESS in atlas (no registry container; services: $services), Postgres-backed: $global_rows global rows in evidence_kind_schemas; atlas container $atlas_cid started $(docker inspect -f '{{.State.StartedAt}}' "$atlas_cid")"
  fi

  # --- C-3: injection mechanism — reversible, and confined -----------
  # The blast-radius claim ("schema-registry surface only") is CHECKED:
  # no package outside the registry + generated query layer reads the
  # table, so revoking the app role's SELECT cannot break another surface.
  local app_has_select other_readers
  app_has_select="$(psql_q "select has_table_privilege('atlas_app','evidence_kind_schemas','select');" 2>/dev/null || echo ERR)"
  other_readers="$(grep -rl "EvidenceKindSchema" --include="*.go" "$REPO_ROOT/internal" "$REPO_ROOT/cmd" 2>/dev/null |
    grep -v "/internal/db/dbx/" | grep -v "/internal/api/schemaregistry/" | grep -v "_test.go" || true)"
  if [[ "$app_has_select" != "t" ]]; then
    check "C-3" "FAIL" "atlas_app does not currently hold SELECT on evidence_kind_schemas (got '$app_has_select') — the stack is not in a clean pre-injection state"
  elif [[ -n "$other_readers" ]]; then
    check "C-3" "FAIL" "evidence_kind_schemas is read outside the registry package, so the injection is not confined: $(printf '%s' "$other_readers" | tr '\n' ' ')"
  else
    check "C-3" "PASS" "injection is 'REVOKE SELECT ON evidence_kind_schemas FROM atlas_app' (reversible by the matching GRANT); atlas_app holds SELECT now; no reader of that table outside internal/api/schemaregistry"
  fi

  # --- C-4: fresh tenant scope + tenant-OWNED control ----------------
  # evidence_records carries a composite FK on (tenant_id, control_id). A
  # control owned by another tenant still yields a receipt — the push acks
  # at stream-commit, before the ledger write — while every insert fails
  # its FK. The ledger then reads zero rows and a count-based check passes
  # vacuously. Slice 356a hit exactly that trap (its decisions log D2).
  local suffix confirmed existing
  suffix="$(printf '%s' "$TENANT_ID" | tr -d '-')"
  psql_q "insert into tenants (id, name, slug)
          values ('$TENANT_ID', 'Chaos fixture — exp7 $RUN_TAG — $suffix', 'chaos-358-$suffix')
          on conflict (id) do nothing;" >/dev/null
  psql_q "insert into controls (
            id, tenant_id, title, description, control_family,
            implementation_type, owner_role, lifecycle_state,
            applicability_expr, bundle_id
          ) values (
            '$CONTROL_ID', '$TENANT_ID',
            'Chaos fixture control (exp7 $RUN_TAG)',
            'Synthesized by scripts/chaos/run-exp7-schema-registry.sh so the slice 335 Experiment 7 push driver has a tenant-scoped control_id. Not a real control.',
            'chaos', 'manual_attested', 'security', 'active', 'true', 'chaos-fixture'
          ) on conflict (id) do nothing;" >/dev/null
  confirmed="$(psql_q "select id from controls where id = '$CONTROL_ID' and tenant_id = '$TENANT_ID';")"
  existing="$(psql_q "select count(*) from evidence_records where tenant_id = '$TENANT_ID';")"
  if [[ -z "$confirmed" ]]; then
    check "C-4" "FAIL" "could not provision a tenant-owned control for $TENANT_ID"
  elif [[ "$existing" != "0" ]]; then
    check "C-4" "FAIL" "tenant $TENANT_ID already holds $existing evidence_records rows — not a fresh scope"
  else
    check "C-4" "PASS" "fresh tenant $TENANT_ID with tenant-OWNED control $CONTROL_ID; 0 pre-existing ledger rows"
  fi

  # --- mint the probe credential -------------------------------------
  # Slice 326 retired the legacy bearer on /v1/*, so the push API takes a
  # JWT. The slice-201 env-gated test endpoint mints one scoped to the
  # fixture tenant; jwtmw synthesizes the ingest credential from its claims.
  local jwt_code
  jwt_code="$(curl -sS -o "$OUT_DIR/jwt.json" -w '%{http_code}' --connect-timeout 3 --max-time 10 \
    -X POST "$HTTP_BASE/v1/test/issue-jwt" -H 'Content-Type: application/json' \
    -d "{\"tenant_id\":\"$TENANT_ID\"}" 2>>"$OUT_DIR/curl.err" || true)"
  [[ "$jwt_code" == "200" ]] || die "could not mint a probe JWT (HTTP $jwt_code) — is ATLAS_TEST_MODE=1 set for this stack?"
  umask 077
  sed -n 's/.*"token":"\([^"]*\)".*/\1/p' "$OUT_DIR/jwt.json" >"$OUT_DIR/jwt"
  chmod 600 "$OUT_DIR/jwt"
  [[ -s "$OUT_DIR/jwt" ]] || die "minted JWT response carried no token"
  HDR_FILE="$OUT_DIR/hdr"
  {
    printf 'Authorization: Bearer %s\n' "$(cat "$OUT_DIR/jwt")"
    printf 'Content-Type: application/json\n'
  } >"$HDR_FILE"
  chmod 600 "$HDR_FILE"

  # --- register the three cold-miss kinds ----------------------------
  # Registered while the registry is UP; never pushed until their own
  # phase, so each resolves only through the registry's DB lookup path.
  local k code
  for k in "$COLD_KIND_STEADY" "$COLD_KIND_INJECTED" "$COLD_KIND_RECOVERED"; do
    code="$(http_code POST "/v1/schemas" "{\"kind\":\"$k\",\"semver\":\"1.0.0\",\"owner\":\"chaos-358\",\"schema\":{\"\$schema\":\"https://json-schema.org/draft/2020-12/schema\",\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"probe\"],\"properties\":{\"probe\":{\"type\":\"string\"}}}}")"
    [[ "$code" == "201" || "$code" == "200" ]] || die "could not register cold-miss kind $k (HTTP $code): $(cat "$OUT_DIR/.body")"
  done
  log "preflight: registered cold-miss kinds $COLD_KIND_STEADY / $COLD_KIND_INJECTED / $COLD_KIND_RECOVERED"

  # --- C-2: design checklist item 2 — known kind is hot-cached -------
  # "Pushed at least once in the current process lifetime." Push it now and
  # require BOTH a 2xx receipt and a ledger landing: a receipt alone would
  # not prove the record traversed the registry's validate path.
  local seed_idem seed_code landed waited
  seed_idem="${RUN_ID}-preflight-known-seed"
  seed_code="$(http_code POST "/v1/evidence:push" "{\"record\":{\"idempotency_key\":\"$seed_idem\",\"evidence_kind\":\"$KNOWN_KIND\",\"schema_version\":\"$KNOWN_SEMVER\",\"control_id\":\"$CONTROL_ID\",\"scope\":[{\"key\":\"env\",\"values\":[\"prod\"]}],\"observed_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"result\":\"pass\",\"payload\":{\"review_id\":\"$seed_idem\",\"completed_by\":\"chaos-358\",\"reviewer_role\":\"security\",\"users_reviewed\":1},\"source_attribution\":{\"actor_type\":\"service\",\"actor_id\":\"chaos-358\"}}}")"
  landed=0
  for ((waited = 0; waited < 30; waited++)); do
    if [[ "$(psql_q "select count(*) from evidence_records where tenant_id='$TENANT_ID' and idempotency_key='$seed_idem';")" == "1" ]]; then
      landed=1
      break
    fi
    sleep 1
  done
  if [[ "$seed_code" != "201" && "$seed_code" != "200" ]]; then
    check "C-2" "FAIL" "known-kind seed push returned HTTP $seed_code"
  elif [[ "$landed" != "1" ]]; then
    check "C-2" "FAIL" "known-kind seed push returned HTTP $seed_code but never reached the ledger within 30s"
  else
    check "C-2" "PASS" "known kind $KNOWN_KIND/$KNOWN_SEMVER pushed in the CURRENT atlas process lifetime (HTTP $seed_code) and landed in the ledger — hot cache holds it"
  fi
}

run_phase() {
  local phase="$1" cold_kind="$2"
  log "phase $phase: $WINDOW_SECONDS s window, cold-miss kind $cold_kind"
  "$PROBE" --http-base "$HTTP_BASE" --jwt-file "$OUT_DIR/jwt" \
    --control "$CONTROL_ID" --run-id "$RUN_ID" --phase "$phase" \
    --duration-seconds "$WINDOW_SECONDS" \
    --known-kind "$KNOWN_KIND" --known-semver "$KNOWN_SEMVER" \
    --unknown-kind "$UNKNOWN_KIND" --cold-kind "$cold_kind" \
    --cold-at-tick "$COLD_AT_TICK" \
    --out "$OUT_DIR/$phase.tsv"
}

drain() {
  # Wait until the ledger row count for the fixture tenant has been stable
  # for DRAIN_STABLE_SECONDS, or DRAIN_MAX_SECONDS elapses.
  #
  # This function's STDOUT is captured by the caller, so its progress line
  # goes to the run log only — a `log` call here would end up inside
  # LEDGER_TOTAL.
  local last stable elapsed now
  last="-1"
  stable=0
  for ((elapsed = 0; elapsed < DRAIN_MAX_SECONDS; elapsed++)); do
    now="$(psql_q "select count(*) from evidence_records where tenant_id='$TENANT_ID';")"
    if [[ "$now" == "$last" ]]; then
      stable=$((stable + 1))
      if [[ "$stable" -ge "$DRAIN_STABLE_SECONDS" ]]; then break; fi
    else
      stable=0
      last="$now"
    fi
    sleep 1
  done
  echo "[$(date -u +%H:%M:%S)] drain: ledger settled at $last rows after ${elapsed}s" >>"$RUN_LOG"
  printf '%s' "$last"
}

main() {
  log "run-exp7 starting — project=$PROJECT http=$HTTP_BASE window=${WINDOW_SECONDS}s run_id=$RUN_ID"
  preflight
  if [[ "$CHECK_FAILED" != "0" ]]; then
    log "REFUSING to inject: the pre-execution checklist did not fully pass. See $CHECKLIST"
    exit 1
  fi
  log "pre-execution checklist fully signed off"

  # --- steady state (BEFORE any injection) ---------------------------
  run_phase steady "$COLD_KIND_STEADY"

  # --- injection ------------------------------------------------------
  INJECT_AT="$(date +%s)"
  log "INJECT: REVOKE SELECT ON evidence_kind_schemas FROM atlas_app"
  psql_do "REVOKE SELECT ON evidence_kind_schemas FROM atlas_app;" >>"$RUN_LOG" 2>&1
  # Confirm the injection actually took, so a no-op revoke cannot be
  # mistaken for a resilient platform.
  local priv
  priv="$(psql_q "select has_table_privilege('atlas_app','evidence_kind_schemas','select');")"
  [[ "$priv" == "f" ]] || die "injection did not take: atlas_app still holds SELECT"
  log "injection confirmed: atlas_app SELECT on evidence_kind_schemas = $priv"

  run_phase injected "$COLD_KIND_INJECTED"

  # --- recovery -------------------------------------------------------
  RESTORE_AT="$(date +%s)"
  log "ROLLBACK: GRANT SELECT ON evidence_kind_schemas TO atlas_app"
  psql_do "GRANT SELECT ON evidence_kind_schemas TO atlas_app;" >>"$RUN_LOG" 2>&1
  # Recovery time = restore -> the registry's own read surface answers 200
  # again. Measured, not assumed: the design's Rollback field claims a
  # restart-free recovery and that claim is exactly what this measures.
  RECOVERED_AT=""
  local waited code
  for ((waited = 0; waited < RECOVERY_DEADLINE_SECONDS * 5; waited++)); do
    code="$(http_code GET "/v1/schemas?limit=1")"
    if [[ "$code" == "200" ]]; then
      RECOVERED_AT="$(date +%s)"
      break
    fi
    sleep 0.2
  done
  if [[ -z "$RECOVERED_AT" ]]; then
    log "RECOVERY: registry read surface did NOT return 200 within ${RECOVERY_DEADLINE_SECONDS}s"
    RECOVERY_SECONDS="unrecovered"
  else
    RECOVERY_SECONDS="$((RECOVERED_AT - RESTORE_AT))"
    log "RECOVERY: registry read surface answered 200 after ${RECOVERY_SECONDS}s (no atlas restart)"
  fi

  run_phase recovered "$COLD_KIND_RECOVERED"

  LEDGER_TOTAL="$(drain)"

  # --- ledger accounting ---------------------------------------------
  psql_q "select idempotency_key from evidence_records where tenant_id='$TENANT_ID' and idempotency_key is not null;" >"$OUT_DIR/landed_keys.txt"
  cat "$OUT_DIR"/steady.tsv "$OUT_DIR"/injected.tsv "$OUT_DIR"/recovered.tsv >"$OUT_DIR/all.tsv"

  summarize
  log "done — summary at $OUT_DIR/summary.txt, comparison table at $OUT_DIR/comparison.md"
}

# q reads one field out of the per-phase/class rollup:
#   q <phase> <class> <field-number>
# Fields: 1 phase, 2 class, 3 requests, 4 2xx, 5 landed, 6 status-code
# histogram, 7 error-code histogram, 8 p50 ms, 9 p95 ms.
q() {
  awk -F'\t' -v p="$1" -v c="$2" -v f="$3" '$1==p && $2==c {print $f}' "$OUT_DIR/rollup.tsv"
}

# summarize joins the per-request TSVs with the ledger's landed-key set and
# writes (a) a machine-readable per-phase/class rollup, (b) the
# steady-vs-injection comparison table, (c) the verdict block.
summarize() {
  awk -F'\t' -v landed_file="$OUT_DIR/landed_keys.txt" '
    BEGIN {
      while ((getline k < landed_file) > 0) { if (k != "") landed[k] = 1 }
    }
    {
      phase = $2; class = $4; key = $5; code = $6; err = $7; ms = $8
      n[phase, class]++
      codes[phase, class, code]++
      errs[phase, class, err]++
      sum[phase, class] += ms
      lat[phase, class, n[phase, class]] = ms
      if (key != "-" && (key in landed)) land[phase, class]++
      if (code ~ /^2/) ok[phase, class]++
      phases[phase] = 1; classes[class] = 1
    }
    END {
      split("steady injected recovered", porder, " ")
      split("known unknown cold registry_list registry_get", corder, " ")
      for (pi = 1; pi <= 3; pi++) {
        p = porder[pi]
        for (ci = 1; ci <= 5; ci++) {
          c = corder[ci]
          if (!((p, c) in n)) continue
          # code histogram
          hist = ""
          for (kk in codes) {
            split(kk, a, SUBSEP)
            if (a[1] == p && a[2] == c) hist = hist (hist == "" ? "" : ",") a[3] "x" codes[kk]
          }
          ehist = ""
          for (kk in errs) {
            split(kk, a, SUBSEP)
            if (a[1] == p && a[2] == c && a[3] != "-") ehist = ehist (ehist == "" ? "" : ",") a[3] "x" errs[kk]
          }
          if (ehist == "") ehist = "-"
          # p50 / p95 over the phase/class latencies
          cnt = n[p, c]
          for (i = 1; i <= cnt; i++) v[i] = lat[p, c, i]
          for (i = 2; i <= cnt; i++) { t = v[i]; j = i - 1; while (j > 0 && v[j] > t) { v[j+1] = v[j]; j-- } v[j+1] = t }
          p50 = v[int((cnt + 1) / 2)]
          p95i = int(cnt * 0.95); if (p95i < 1) p95i = 1
          p95 = v[p95i]
          printf "%s\t%s\t%d\t%d\t%d\t%s\t%s\t%d\t%d\n", p, c, cnt, ok[p, c] + 0, land[p, c] + 0, hist, ehist, p50, p95
          split("", v)
        }
      }
    }
  ' "$OUT_DIR/all.tsv" >"$OUT_DIR/rollup.tsv"

  {
    echo "# Slice 335 Experiment 7 — schema-registry unavailable — run $RUN_ID"
    echo
    echo "Local docker-compose project \`$PROJECT\` only. Window: ${WINDOW_SECONDS}s per phase."
    echo "Injection: \`REVOKE SELECT ON evidence_kind_schemas FROM atlas_app\` (in-process registry; no container to stop)."
    echo
    echo "| phase | probe class | requests | 2xx | landed in ledger | status codes | error codes | p50 ms | p95 ms |"
    echo "| ----- | ----------- | -------- | --- | ---------------- | ------------ | ----------- | ------ | ------ |"
    awk -F'\t' '{printf "| %s | %s | %d | %d | %d | %s | %s | %d | %d |\n", $1, $2, $3, $4, $5, $6, $7, $8, $9}' "$OUT_DIR/rollup.tsv"
    echo
    echo "\`landed in ledger\` counts rows actually present in \`evidence_records\` for the fixture tenant."
    echo "Registry-read classes push nothing, so their landed count is 0 by construction."
  } >"$OUT_DIR/comparison.md"

  # --- verdicts -------------------------------------------------------
  # Each verdict quotes the design text it evaluates, then states what was
  # measured. A falsified hypothesis is a result, not a failure.
  local steady_unknown_codes injected_unknown_codes recovered_unknown_codes
  local injected_known_req injected_known_ok injected_known_landed
  local steady_cold_codes injected_cold_codes recovered_cold_codes
  local steady_cold_landed injected_cold_landed recovered_cold_landed
  steady_unknown_codes="$(q steady unknown 6)"
  injected_unknown_codes="$(q injected unknown 6)"
  recovered_unknown_codes="$(q recovered unknown 6)"
  injected_known_req="$(q injected known 3)"
  injected_known_ok="$(q injected known 4)"
  injected_known_landed="$(q injected known 5)"
  steady_cold_codes="$(q steady cold 6)"
  injected_cold_codes="$(q injected cold 6)"
  recovered_cold_codes="$(q recovered cold 6)"
  steady_cold_landed="$(q steady cold 5)"
  injected_cold_landed="$(q injected cold 5)"
  recovered_cold_landed="$(q recovered cold 5)"

  {
    echo "Slice 335 Experiment 7 — verdicts (run $RUN_ID, $(date -u +%Y-%m-%dT%H:%M:%SZ))"
    echo
    echo "Pre-execution checklist:"
    cat "$CHECKLIST"
    echo
    echo "Window per phase: ${WINDOW_SECONDS}s. Injection at epoch $INJECT_AT, restore at epoch $RESTORE_AT."
    echo "Recovery to a 200 from the registry read surface: ${RECOVERY_SECONDS}s (no atlas restart)."
    echo "Ledger rows for the fixture tenant after drain: $LEDGER_TOTAL"
    echo
    echo "V-1  design steady state: \"push for an unknown evidence_kind returns 400 with"
    echo "     {error: evidence_kind_not_found}\""
    echo "     MEASURED (steady phase, unknown class): $steady_unknown_codes"
    echo
    echo "V-2  design expected outcome: \"unknown-kind push during outage: 503 with body"
    echo "     {error: schema_registry_unavailable, correlation_id: ...} — distinguishable"
    echo "     from 400 {error: evidence_kind_not_found}\""
    echo "     MEASURED (injected phase, unknown class): $injected_unknown_codes"
    echo
    echo "V-3  design expected outcome: \"after restore: unknown-kind push transitions to 400\""
    echo "     MEASURED (recovered phase, unknown class): $recovered_unknown_codes"
    echo
    echo "V-4  design hypothesis conjunct 2 / expected outcome: \"known-kind push: 200/202"
    echo "     (cache hit serves it)\""
    echo "     MEASURED (injected phase, known class): $injected_known_req requests,"
    echo "     $injected_known_ok 2xx, $injected_known_landed landed in the ledger"
    echo
    echo "V-5  design abort criterion: \"unknown-kind push returns 2xx (falsifies the"
    echo "     fail-fast claim)\""
    echo "     MEASURED: steady=$steady_unknown_codes injected=$injected_unknown_codes recovered=$recovered_unknown_codes"
    echo
    echo "V-6  slice 358's hot-cache-vs-cold-miss distinction: a VALID tenant-private kind"
    echo "     that must resolve through the registry's DB lookup."
    echo "     MEASURED  steady:    codes=$steady_cold_codes    landed=$steady_cold_landed"
    echo "     MEASURED  injected:  codes=$injected_cold_codes  landed=$injected_cold_landed"
    echo "     MEASURED  recovered: codes=$recovered_cold_codes landed=$recovered_cold_landed"
    echo
    echo "Per-phase rollup (phase, class, requests, 2xx, landed, codes, error codes, p50ms, p95ms):"
    cat "$OUT_DIR/rollup.tsv"
  } >"$OUT_DIR/summary.txt"

  cat "$OUT_DIR/summary.txt"
}

main "$@"
