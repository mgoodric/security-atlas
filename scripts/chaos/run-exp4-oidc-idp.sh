#!/usr/bin/env bash
#
# run-exp4-oidc-idp.sh — orchestrator for slice 335 Experiment 4,
# "OIDC IdP unavailable". Executed by slice 357a.
#
# Runs the design's Method against the LOCAL docker-compose stack:
#
#   1. start docker-compose with a containerized IdP (Dex)   (step 1)
#   2. mint a JWT via the normal flow; capture it            (step 2)
#   3. verify the JWT works on a protected endpoint          (step 3)
#   4. inject: detach the IdP container from the network     (step 4)
#   5. test, during the outage                               (step 5)
#        - existing JWT on a protected endpoint  -> should still succeed
#        - new login via /oauth/authorize        -> should fail friendly
#        - atlas-issued JWT key-rotation         -> should keep working
#   6. restore the network; verify new logins resume         (step 6)
#
# The design is the contract. This script does not reinterpret it: the
# containerized-IdP requirement, the network-detach injection, the three
# during-outage checks, and the "new logins resume within 30s" recovery
# claim are read straight out of
# docs/audits/335-chaos-experiment-design.md §Experiment 4.
#
# WHAT THE DESIGN DOES NOT SPECIFY, AND WHAT THIS SCRIPT PICKS
#
# Experiment 4's Method names no hold duration (Experiment 1 names five
# minutes, Experiment 2 names ten; Experiment 4 names none). This run
# holds for five minutes — the shorter of the two durations the design
# does name — with a symmetric five-minute steady-state window captured
# BEFORE injection so the two halves are comparable tick for tick. The
# choice is recorded here rather than buried, because it is the one
# parameter not inherited from the design.
#
# THREE ARMS: TWO FROM THE DESIGN'S Variable FIELD, ONE FROM THE RP'S
# ACTUAL IMPLEMENTATION
#
# The design names two injection mechanisms:
#
#   "Network egress from atlas to IdP URL, perturbed via
#    `iptables -A OUTPUT -d <idp-ip> -j DROP` (or via docker-compose
#    network isolation: detach atlas from the network that reaches the
#    simulated IdP container)."
#
# They do not fail the same way. Detaching the container removes its
# docker-DNS entry, so the failure is a fast NAME-RESOLUTION error;
# dropping egress leaves DNS intact, so the failure is a CONNECT
# TIMEOUT. The design's second abort criterion — "atlas crashes on
# IdP-unreachable (signals a missing timeout on the OIDC discovery
# refresh)" — is specifically about the timeout shape, and a
# detach-only run cannot speak to it. So the run executes both:
#
#   arm A  detach     `docker network disconnect` — the design's
#                     parenthetical mechanism; DNS-shaped failure
#   arm B  blackhole  `iptables -A OUTPUT -d <idp-ip> -j DROP` applied
#                     inside atlas's own network namespace — the
#                     design's first-named mechanism; timeout-shaped
#                     failure
#
#   arm C  cold cache  detach the IdP FIRST, then restart atlas, so atlas
#                      comes up having never resolved this issuer — and
#                      the pre-restart JWT is now older than the process
#                      serving it.
#
# See the block comment above ARMS for why arm C is here and what it
# actually measures on this deployment. None of the design's fields are
# altered by any arm — slice 335 owns the design; the arms are initial
# conditions, not redesigns.
#
# Same steady state, same hold duration, same probes, in every arm. No arm
# substitutes for another.
#
# WHAT THIS RUN FOUND BEFORE IT EVEN INJECTED (see gate C-5)
#
# cmd/atlas/main.go wires the OIDC authenticator as
# `oidc.New(localModeIdpResolver{})`, and that resolver's ResolveIdp
# returns ErrUnknownIdp unconditionally. The docker-compose deployment
# therefore has NO functioning OIDC relying-party surface: /auth/oidc/login
# 400s on every request regardless of whether any IdP is reachable, and
# the oidc_idp_configs row written by PATCH /v1/admin/sso is never read by
# the running binary. The design's new-login check is consequently VACUOUS
# on this deployment — not satisfied, not violated, but unexercisable.
# The run proceeds anyway: the design's other two checks (existing JWT
# survives, key rotation continues) are fully live, and a documented null
# result with its cause named is worth more than a refusal to run.
#
# MEASURING THE INJECTION ITSELF, NOT JUST ITS EFFECT
#
# A chaos run that only watches the application can pass vacuously: if
# the application never touched the dependency, removing the dependency
# changes nothing and the hypothesis "holds" for the wrong reason. So
# every tick also probes the IdP's discovery document from INSIDE the
# atlas container's own network namespace (a sidecar started with
# `--network container:<atlas>`), which is the exact egress path the OIDC
# RP's discovery call would take. If that probe does not flip from
# reachable to unreachable at the injection boundary and back on
# recovery, the run is not evidence of anything and says so.
#
# SCOPE BOUNDARY (slice 335 §"Scope discipline" / P0-335-2, slice 357
# P0-1 / P0-2) — LOAD-BEARING. Every action is scoped to one named local
# docker-compose project:
#
#   - the script refuses to run unless the atlas HTTP endpoint host is a
#     loopback literal;
#   - the IdP container to detach is resolved via `docker compose ps -q
#     dex` against the compose files passed in, so the detach cannot
#     reach a container belonging to atlas-edge, a hosted deployment, or
#     any unrelated stack;
#   - the script refuses to run unless the configured IdP issuer host is
#     the in-compose service name, so it cannot be pointed at a real
#     identity provider (slice 357 P0-1, design checklist item 1);
#   - no hosted or edge endpoint appears anywhere in scripts/chaos/.
#
# ABORT CRITERIA (design, verbatim):
#   - Existing JWT verification fails (this falsifies the design claim —
#     local key verification should never depend on the IdP).
#   - OR atlas crashes on IdP-unreachable (signals a missing timeout on
#     the OIDC discovery refresh).
# Both are evaluated on EVERY tick of the injection phase. Either one
# restores the network immediately (the design's Rollback) and ends the
# injection early; the run still reports, and the trip is recorded as a
# falsification rather than swept up as an operational hiccup.
#
# PRE-EXECUTION CHECKLIST (design, verbatim):
#   [ ] Use a containerized IdP (Dex). Do NOT target a real external IdP.
#   [ ] Have an active JWT minted BEFORE injection; record its `exp`
#       claim for the verification step.
# Both are executed by preflight() below against the running stack, and
# echoed into the run log so the decisions log signs them off against
# real output rather than against an assertion. preflight() adds further
# gates (marked "added" in its output) because the design's two items are
# not on their own enough to make the run meaningful — most importantly
# that the key-rotation cron is observed ticking BEFORE injection, since
# "no rotation errors during the outage" is vacuous if no rotation ever
# fires. Injection does not start until every gate reads PASS.
#
# Usage:
#   scripts/chaos/run-exp4-oidc-idp.sh --out-dir /tmp/exp4 [--run-tag r1]
#     [--steady-seconds 300] [--inject-seconds 300]
#
# Exit: 0 ran to completion (falsified or not — read the summary),
#       1 refused (guard tripped) or aborted before injection,
#       2 environment error.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker/docker-compose.yml"
OVERLAY_FILE="$REPO_ROOT/scripts/chaos/exp4-idp-overlay.yml"
DEX_CONFIG="$REPO_ROOT/scripts/chaos/exp4-dex-config.yaml"
ENV_FILE="$REPO_ROOT/deploy/docker/.env"

OUT_DIR=""
RUN_TAG="r1"

# --- design constants. Do not tune to make a run look better. ---------
# Hold duration: see the header note — the design names none for
# Experiment 4; five minutes is the shorter of the two it names elsewhere.
STEADY_SECONDS=300
INJECT_SECONDS=300
# Post-recovery observation, long enough to cover the design's "new
# logins resume within 30s (OIDC discovery refresh interval)" claim with
# margin.
POST_RECOVERY_SECONDS=60
RECOVERY_DEADLINE_SECONDS=120
PROBE_INTERVAL_SECONDS=5
# The IdP fixture's issuer, as atlas would reach it. Must match
# exp4-dex-config.yaml's `issuer`.
IDP_ISSUER="http://dex:5556/dex"
IDP_SERVICE="dex"
# Every probe is bounded so one hung request cannot stretch a tick past
# the interval and desynchronise the two phases' tick counts.
CONNECT_TIMEOUT=3
MAX_TIME=4

NETPROBE_NAME="security-atlas-exp4-netprobe"
# netshoot carries both curl (the discovery probe) and iptables (arm B's
# injection). One sidecar in atlas's network namespace does both jobs, so
# the thing that measures the outage and the thing that causes it share
# exactly one view of the network.
NETPROBE_IMAGE="nicolaka/netshoot:latest"

die() {
  echo "run-exp4: $1" >&2
  exit "${2:-2}"
}

log() { echo "[$(date -u +%H:%M:%S)] $*" | tee -a "${RUN_LOG:-/dev/null}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --out-dir)
    OUT_DIR="${2:-}"
    shift 2
    ;;
  --run-tag)
    RUN_TAG="${2:-}"
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
  --compose-file)
    COMPOSE_FILE="${2:-}"
    shift 2
    ;;
  --env-file)
    ENV_FILE="${2:-}"
    shift 2
    ;;
  *)
    die "unknown argument: $1" 1
    ;;
  esac
done

[[ -n "$OUT_DIR" ]] || die "--out-dir is required" 1
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ -f "$OVERLAY_FILE" ]] || die "overlay file not found: $OVERLAY_FILE"
[[ -f "$DEX_CONFIG" ]] || die "dex config not found: $DEX_CONFIG"
[[ -f "$ENV_FILE" ]] || die "env file not found: $ENV_FILE (copy deploy/docker/.env.example)"

mkdir -p "$OUT_DIR"
RUN_LOG="$OUT_DIR/run.log"
: >"$RUN_LOG"

# Secrets are read from the env-file, never from argv (argv is
# world-readable via `ps` on this platform).
set -a
# shellcheck disable=SC1090 # path is a runtime argument, not a literal
source "$ENV_FILE"
set +a

ATLAS_HTTP_PORT="${ATLAS_HTTP_PORT:-8080}"
HTTP_BASE="http://127.0.0.1:${ATLAS_HTTP_PORT}"
TENANT_ID="${ATLAS_BOOTSTRAP_TENANT:-00000000-0000-4000-8000-000000000001}"
PG_DB="${POSTGRES_DB:-security_atlas}"

export EXP4_DEX_CONFIG="$DEX_CONFIG"

compose() {
  docker compose -f "$COMPOSE_FILE" -f "$OVERLAY_FILE" --env-file "$ENV_FILE" "$@"
}

# --- scope guards -----------------------------------------------------
# Guard 1: the atlas endpoint this script probes must be loopback. This
# is what makes it impossible to aim the run at atlas-edge or a hosted
# instance even by accident.
case "${HTTP_BASE}" in
http://127.0.0.1:* | http://localhost:* | http://[::1]:*) ;;
*) die "REFUSED: atlas endpoint '$HTTP_BASE' is not a loopback literal (slice 335 P0-335-2)" 1 ;;
esac

# Guard 2: the IdP issuer must be the in-compose service name. `dex`
# resolves only on the project network; a real IdP hostname would not
# match and the run refuses. This is the design's checklist item 1
# ("Do NOT target a real external IdP") enforced in the tooling rather
# than left to operator care.
case "$IDP_ISSUER" in
"http://${IDP_SERVICE}:"*) ;;
*) die "REFUSED: IdP issuer '$IDP_ISSUER' is not the in-compose '$IDP_SERVICE' service (slice 357 P0-1)" 1 ;;
esac

psql_q() { docker exec "$PG_CID" psql -U postgres -d "$PG_DB" -Atc "$1"; }

# curl_code_ms <outfile> <curl args...> -> "<http_code> <total_ms>"
# Never fails the script: a connection refusal is data, not an error.
curl_code_ms() {
  local out="$1"
  shift
  local w
  w="$(curl -sS -o "$out" -w '%{http_code} %{time_total}' \
    --connect-timeout "$CONNECT_TIMEOUT" --max-time "$MAX_TIME" "$@" 2>>"$OUT_DIR/curl.err" || true)"
  if [[ -z "$w" ]]; then
    echo "000 0"
    return 0
  fi
  awk '{printf "%s %d\n", $1, ($2*1000)}' <<<"$w"
}

# jwt_claim <jwt> <claim> — decode the payload segment. base64url with
# padding restored (an unpadded segment decodes SHORT and silently loses
# the tail of the JSON, so the padding is load-bearing, not cosmetic);
# jq reads the claim. Signature is NOT verified here — this is for
# recording `exp`, and the platform is the thing that verifies.
jwt_claim() {
  local payload="${1#*.}"
  payload="${payload%%.*}"
  local pad=$(((4 - ${#payload} % 4) % 4))
  local padding='==='
  printf '%s%s' "$payload" "${padding:0:$pad}" |
    tr '_-' '/+' | base64 -d 2>/dev/null | jq -r ".$2 // empty"
}

# --- fixture provisioning --------------------------------------------

bring_up() {
  log "bringing up the local stack with the containerized IdP overlay"
  compose up -d >>"$RUN_LOG" 2>&1 || die "compose up failed — see $RUN_LOG"

  ATLAS_CID="$(compose ps -q atlas)"
  PG_CID="$(compose ps -q postgres)"
  DEX_CID="$(compose ps -q "$IDP_SERVICE")"
  [[ -n "$ATLAS_CID" ]] || die "atlas container not found in this compose project"
  [[ -n "$PG_CID" ]] || die "postgres container not found in this compose project"
  [[ -n "$DEX_CID" ]] || die "$IDP_SERVICE container not found in this compose project"

  # Guard 3: the container we are about to detach is the one this
  # compose project owns. Resolved from the project, never from a name
  # the caller supplied.
  log "resolved containers: atlas=${ATLAS_CID:0:12} postgres=${PG_CID:0:12} ${IDP_SERVICE}=${DEX_CID:0:12}"

  # The project network the injection removes the IdP from.
  IDP_NETWORK="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}' "$DEX_CID" | head -1)"
  [[ -n "$IDP_NETWORK" ]] || die "could not resolve the IdP container's network"
  log "IdP is attached to network: $IDP_NETWORK"

  local deadline=$((SECONDS + 240))
  until [[ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$HTTP_BASE/health" || true)" == "200" ]]; do
    ((SECONDS < deadline)) || die "atlas /health did not reach 200 within 240s"
    sleep 3
  done
  log "atlas /health is 200"
}

start_netprobe() {
  docker rm -f "$NETPROBE_NAME" >/dev/null 2>&1 || true
  # Shares the atlas container's network namespace, so this probe's view
  # of the IdP IS atlas's view of the IdP — same DNS resolver, same
  # routes, same attachments. atlas itself is distroless (no shell, no
  # curl), which is why the measurement is taken from a sidecar rather
  # than from inside atlas.
  #
  # NET_ADMIN is what lets arm B install its DROP rule in that shared
  # namespace. The capability is scoped to this sidecar and to the
  # namespace atlas already owns — it grants nothing on the host.
  docker run -d --rm --name "$NETPROBE_NAME" \
    --network "container:$ATLAS_CID" --cap-add NET_ADMIN \
    --entrypoint sleep "$NETPROBE_IMAGE" infinity >>"$RUN_LOG" 2>&1 ||
    die "could not start the network probe sidecar in atlas's namespace"
  log "network-probe sidecar running in atlas's network namespace (NET_ADMIN for arm B)"
}

stop_netprobe() { docker rm -f "$NETPROBE_NAME" >/dev/null 2>&1 || true; }

# resolve_dex_ip — arm B's DROP rule targets the IdP by address, and a
# re-attach after arm A can hand the container a different address, so
# this is re-resolved rather than cached from bring-up.
resolve_dex_ip() {
  DEX_IP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$DEX_CID")"
  [[ -n "$DEX_IP" ]] || die "could not resolve the IdP container's IP address"
  log "IdP container address: $DEX_IP"
}

# mint_jwt — design step 2, "Mint a JWT via normal flow; capture it".
# The normal flow for this local-mode bundle is POST /auth/local/login,
# which establishes the session cookie AND (slice 209) returns an
# atlas-issued JWT signed by the local AS.
mint_jwt() {
  local body="$OUT_DIR/login.json"
  local code
  code="$(curl -sS -o "$body" -w '%{http_code}' --max-time 10 \
    -H 'Content-Type: application/json' \
    -d "{\"tenant_id\":\"$TENANT_ID\",\"email\":\"${ATLAS_DEFAULT_USER_EMAIL}\",\"password\":\"${ATLAS_DEFAULT_USER_PASSWORD}\"}" \
    "$HTTP_BASE/auth/local/login" || true)"
  [[ "$code" == "200" ]] || die "mint_jwt: /auth/local/login returned $code (see $body)"
  JWT="$(jq -r '.token // empty' <"$body")"
  [[ -n "$JWT" ]] || die "mint_jwt: no token in the login response — the local AS is not wired"
  JWT_EXP="$(jwt_claim "$JWT" exp)"
  JWT_IAT="$(jwt_claim "$JWT" iat)"
  [[ -n "$JWT_EXP" ]] || die "mint_jwt: could not read the exp claim"
  # The JWT is a credential: mode 0600, never echoed to stdout, never
  # passed on argv.
  umask 077
  printf '%s' "$JWT" >"$OUT_DIR/jwt.token"
  umask 022
  log "minted JWT: iat=$JWT_IAT exp=$JWT_EXP ($(date -u -r "$JWT_EXP" +%Y-%m-%dT%H:%M:%SZ))"
}

# ensure_idp_config — the experiment's premise is that an IdP IS
# configured. Written through the shipped operator surface
# (PATCH /v1/admin/sso, slice 062) so the row is created exactly as an
# operator would create it.
#
# The credential is the admin JWT from mint_jwt, NOT the bootstrap
# credential-store token: /v1/admin/sso sits behind jwtmw, which rejects
# a credstore bearer outright ("authorization must be `Bearer <token>`").
# That ordering dependency is why mint_jwt runs first in main().
ensure_idp_config() {
  local body="$OUT_DIR/idp-config.json"
  local code
  code="$(curl -sS -o "$body" -w '%{http_code}' --max-time 10 -X PATCH \
    -H "Authorization: Bearer ${JWT}" \
    -H 'Content-Type: application/json' \
    -d "{\"issuer_url\":\"$IDP_ISSUER\",\"client_id\":\"atlas-chaos-exp4\",\"client_secret\":\"chaos-exp4-client-secret-local-only\",\"redirect_url\":\"http://localhost:${ATLAS_HTTP_PORT}/auth/oidc/callback\",\"allowed_email_domains\":[\"example.com\"]}" \
    "$HTTP_BASE/v1/admin/sso" || true)"
  IDP_CONFIG_CODE="$code"
  log "IdP config PATCH /v1/admin/sso -> $code"
}

# ensure_oauth_client — /oauth/authorize validates client_id +
# redirect_uri against the registry BEFORE it reaches the session check.
# Without a registered pair the probe would measure a parameter-
# validation 400 and never exercise the login path at all.
ensure_oauth_client() {
  local redirect="http://localhost:${ATLAS_HTTP_PORT}/oauth/callback"
  OAUTH_REDIRECT="$redirect"
  local existing
  existing="$(psql_q "SELECT client_id FROM oauth_client_redirect_uris WHERE redirect_uri = '$redirect' LIMIT 1")"
  if [[ -n "$existing" ]]; then
    OAUTH_CLIENT_ID="$existing"
    log "reusing registered OAuth client for the authorize probe: $OAUTH_CLIENT_ID"
    return 0
  fi
  # `oauth issue-client` prints the client_secret on stdout as well. It is
  # captured into a local and never written to the run log or to disk —
  # the authorize probe needs only the client_id (the authorize endpoint
  # takes no secret).
  local issued
  issued="$(compose run --rm --entrypoint atlas-cli atlas-bootstrap \
    oauth issue-client "exp4-authorize-probe-$RUN_TAG" 2>>"$RUN_LOG" || true)"
  OAUTH_CLIENT_ID="$(sed -n 's/^client_id: *//p' <<<"$issued" | tr -d '\r' | head -1)"
  issued=""
  [[ -n "$OAUTH_CLIENT_ID" ]] || die "could not issue an OAuth client for the authorize probe"
  compose run --rm --entrypoint atlas-cli atlas-bootstrap \
    oauth add-redirect-uri "$OAUTH_CLIENT_ID" "$redirect" >>"$RUN_LOG" 2>&1 ||
    die "could not register the authorize probe redirect_uri"
  log "issued OAuth client for the authorize probe: $OAUTH_CLIENT_ID"
}

# --- probes -----------------------------------------------------------
# Each probe writes one TSV row:
#   phase  tick  t_s  probe  code  ms  detail

authorize_url() {
  printf '%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=openid&state=exp4&code_challenge=%s&code_challenge_method=S256&tenant_id=%s' \
    "$HTTP_BASE" "$OAUTH_CLIENT_ID" \
    "$(jq -rn --arg v "$OAUTH_REDIRECT" '$v|@uri')" \
    "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" "$TENANT_ID"
}

probe_tick() {
  local phase="$1" tick="$2" t_s="$3"
  local tsv="$OUT_DIR/$phase.tsv"
  local code ms detail body

  # P1 — existing JWT on a protected endpoint. The design's central
  # claim: key verification is local, so this must not care that the
  # IdP is gone. Also the design's first abort criterion.
  body="$OUT_DIR/.p1.body"
  read -r code ms < <(curl_code_ms "$body" -H "Authorization: Bearer $JWT" "$HTTP_BASE/v1/me")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$tick" "$t_s" existing_jwt "$code" "$ms" "-" >>"$tsv"
  LAST_JWT_CODE="$code"

  # P2 — a NEW authorization attempt. No session cookie, so this is the
  # first step of a new login. Records the status AND the Location, since
  # the whole question is where a new login goes when the IdP is down.
  body="$OUT_DIR/.p2.body"
  local hdr="$OUT_DIR/.p2.hdr"
  read -r code ms < <(curl_code_ms "$body" -D "$hdr" "$(authorize_url)")
  detail="$(sed -n 's/^[Ll]ocation: //p' "$hdr" | tr -d '\r\t' | head -1 | cut -c1-200)"
  [[ -n "$detail" ]] || detail="$(tr -d '\n\t' <"$body" | cut -c1-200)"
  [[ -n "$detail" ]] || detail="-"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$tick" "$t_s" authorize "$code" "$ms" "$detail" >>"$tsv"
  cp "$body" "$OUT_DIR/last-authorize-body.$phase.txt" 2>/dev/null || true

  # P3 — the OIDC RP login entry point the authorize redirect points at.
  # This is the surface that would actually talk to the IdP.
  body="$OUT_DIR/.p3.body"
  read -r code ms < <(curl_code_ms "$body" "$HTTP_BASE/auth/oidc/login?tenant_id=$TENANT_ID&idp=primary")
  detail="$(tr -d '\n\t' <"$body" | cut -c1-160)"
  [[ -n "$detail" ]] || detail="-"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$tick" "$t_s" oidc_login "$code" "$ms" "$detail" >>"$tsv"
  cp "$body" "$OUT_DIR/last-oidc-login-body.$phase.txt" 2>/dev/null || true

  # P4 — the IdP's discovery document, probed from atlas's own network
  # namespace. This is the injection's own witness: if it does not flip,
  # the run proves nothing.
  local raw
  raw="$(docker exec "$NETPROBE_NAME" curl -s -o /dev/null \
    -w '%{http_code} %{time_total}' --connect-timeout "$CONNECT_TIMEOUT" --max-time "$MAX_TIME" \
    "$IDP_ISSUER/.well-known/openid-configuration" 2>/dev/null || echo "000 0")"
  code="$(awk '{print $1}' <<<"$raw")"
  ms="$(awk '{printf "%d", $2*1000}' <<<"$raw")"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$tick" "$t_s" idp_discovery "${code:-000}" "${ms:-0}" "-" >>"$tsv"
  LAST_IDP_CODE="${code:-000}"

  # P5 — liveness. The design's second abort criterion is "atlas crashes
  # on IdP-unreachable".
  read -r code ms < <(curl_code_ms /dev/null "$HTTP_BASE/health")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$tick" "$t_s" health "$code" "$ms" "-" >>"$tsv"
  LAST_HEALTH_CODE="$code"
}

run_phase() {
  local phase="$1" seconds="$2" abortable="$3"
  local tsv="$OUT_DIR/$phase.tsv"
  : >"$tsv"
  local start=$SECONDS tick=0
  PHASE_ABORTED=""
  log "phase '$phase': ${seconds}s at ${PROBE_INTERVAL_SECONDS}s cadence"
  while ((SECONDS - start < seconds)); do
    probe_tick "$phase" "$tick" "$((SECONDS - start))"
    if [[ "$abortable" == "abortable" ]]; then
      if [[ "$LAST_JWT_CODE" != "200" ]]; then
        PHASE_ABORTED="existing JWT returned $LAST_JWT_CODE on a protected endpoint"
        log "ABORT CRITERION TRIPPED: $PHASE_ABORTED"
        return 0
      fi
      if [[ "$LAST_HEALTH_CODE" != "200" ]]; then
        PHASE_ABORTED="atlas /health returned $LAST_HEALTH_CODE"
        log "ABORT CRITERION TRIPPED: $PHASE_ABORTED"
        return 0
      fi
    fi
    tick=$((tick + 1))
    sleep "$PROBE_INTERVAL_SECONDS"
  done
}

# --- key-rotation observation ----------------------------------------
# Counts what the rotation cron did inside a window. A rotation emits
# `atlas: audit_event=key_rotation` (cmd/atlas/main.go doKeyRotation).
#
# The failure count matches the FOUR failure paths in that function by
# name, not the `key-rotation:` prefix generally. The prefix is not a
# failure marker: doKeyRotation's prune-success path also logs
# `atlas: key-rotation: pruned keys past overlap window` at Info. A
# prefix match would score that success as an error and hand the
# design's third check ("key-rotation cron: continues, no error log")
# a false failure. The four real failure lines are:
#
#   key-rotation: read active key   (Error)
#   key-rotation: rotate failed     (Error)
#   key-rotation: read new key      (Error)
#   key-rotation: prune failed      (Warn)
ROTATION_FAIL_RE='key-rotation: (read active key|rotate failed|read new key|prune failed)'

rotation_stats() {
  local since="$1" until_="$2" out="$3"
  docker logs --since "$since" --until "$until_" "$ATLAS_CID" >"$out" 2>&1 || true
  local ok errs
  ok="$(grep -c 'audit_event=key_rotation' "$out" || true)"
  errs="$(grep -Ec "$ROTATION_FAIL_RE" "$out" || true)"
  echo "${ok:-0} ${errs:-0}"
}

# --- preflight --------------------------------------------------------
gate() {
  local id="$1" item="$2" source="$3" result="$4" note="$5"
  printf '%s\t%s\t%s\t%s\t%s\n' "$id" "$item" "$source" "$result" "$note" >>"$OUT_DIR/preflight.tsv"
  log "  $id [$result] $item — $note"
  [[ "$result" != "FAIL" ]] || PREFLIGHT_FAILED=1
}

preflight() {
  : >"$OUT_DIR/preflight.tsv"
  PREFLIGHT_FAILED=0
  log "pre-execution checklist (slice 335 §Experiment 4)"

  # S-1 (added, operational)
  local running
  running="$(compose ps --format '{{.Service}} {{.State}}' | awk '$2=="running"' | wc -l | tr -d ' ')"
  local health
  health="$(docker inspect -f '{{.State.Health.Status}}' "$ATLAS_CID" 2>/dev/null || echo none)"
  if [[ "$health" == "healthy" ]]; then
    gate S-1 "Stack up and atlas healthy before injecting" "added (operational)" PASS \
      "$running services running, atlas health=$health"
  else
    gate S-1 "Stack up and atlas healthy before injecting" "added (operational)" FAIL \
      "atlas health=$health"
  fi

  # C-1 — design checklist item 1.
  local dex_image
  dex_image="$(docker inspect -f '{{.Config.Image}}' "$DEX_CID")"
  if [[ "$dex_image" == *dex* && "$IDP_ISSUER" == "http://${IDP_SERVICE}:"* ]]; then
    gate C-1 "Containerized IdP, NOT a real external IdP" design PASS \
      "image=$dex_image issuer=$IDP_ISSUER (in-compose service name; resolves nowhere else)"
  else
    gate C-1 "Containerized IdP, NOT a real external IdP" design FAIL \
      "image=$dex_image issuer=$IDP_ISSUER"
  fi

  # C-1b (added) — the IdP fixture actually serves OIDC discovery. A
  # container that was never up cannot be meaningfully taken down.
  local disc=000 disc_deadline=$((SECONDS + 60))
  while ((SECONDS < disc_deadline)); do
    disc="$(docker exec "$NETPROBE_NAME" curl -s -o /dev/null -w '%{http_code}' \
      --max-time 5 "$IDP_ISSUER/.well-known/openid-configuration" 2>/dev/null || echo 000)"
    [[ "$disc" != "200" ]] || break
    sleep 3
  done
  if [[ "$disc" == "200" ]]; then
    gate C-1b "IdP serves OIDC discovery, from atlas's own network namespace" "added (see run header)" PASS \
      "GET $IDP_ISSUER/.well-known/openid-configuration -> 200"
  else
    gate C-1b "IdP serves OIDC discovery, from atlas's own network namespace" "added (see run header)" FAIL \
      "discovery not reachable before injection (last code $disc)"
  fi

  # C-1c (added) — the platform is configured to USE that IdP.
  local cfg
  cfg="$(psql_q "SELECT issuer_url FROM oidc_idp_configs WHERE tenant_id = '$TENANT_ID' AND name = 'primary'")"
  if [[ "$cfg" == "$IDP_ISSUER" ]]; then
    gate C-1c "An IdP config row exists for the tenant, pointing at the fixture" "added" PASS \
      "oidc_idp_configs(primary).issuer_url=$cfg (written via PATCH /v1/admin/sso -> $IDP_CONFIG_CODE)"
  else
    gate C-1c "An IdP config row exists for the tenant, pointing at the fixture" "added" FAIL \
      "row issuer_url='${cfg:-<none>}' (PATCH /v1/admin/sso -> $IDP_CONFIG_CODE)"
  fi

  # C-2 — design checklist item 2.
  gate C-2 "Active JWT minted BEFORE injection; exp claim recorded" design PASS \
    "exp=$JWT_EXP ($(date -u -r "$JWT_EXP" +%Y-%m-%dT%H:%M:%SZ)), iat=$JWT_IAT"

  # C-2b (added) — the JWT actually authenticates. Otherwise "it still
  # works during the outage" would be measuring nothing.
  local me
  me="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -H "Authorization: Bearer $JWT" "$HTTP_BASE/v1/me" || true)"
  if [[ "$me" == "200" ]]; then
    gate C-2b "The minted JWT authenticates on a protected endpoint" "added" PASS \
      "GET /v1/me -> 200"
  else
    gate C-2b "The minted JWT authenticates on a protected endpoint" "added" FAIL "GET /v1/me -> $me"
  fi

  # C-2c (added) — the token must outlive the run, or a late failure
  # would be attributable to expiry rather than to the injection.
  local need=$((STEADY_SECONDS + INJECT_SECONDS + POST_RECOVERY_SECONDS + RECOVERY_DEADLINE_SECONDS + 120))
  local slack=$((JWT_EXP - $(date -u +%s)))
  if ((slack > need)); then
    gate C-2c "JWT TTL outlives the whole run window" "added" PASS \
      "${slack}s remaining vs ${need}s needed — expiry cannot confound a late failure"
  else
    gate C-2c "JWT TTL outlives the whole run window" "added" FAIL \
      "${slack}s remaining vs ${need}s needed"
  fi

  # C-3 (added) — /oauth/authorize must reach the login path, not die at
  # parameter validation.
  local acode ahdr="$OUT_DIR/preflight-authorize.hdr"
  acode="$(curl -s -o /dev/null -D "$ahdr" -w '%{http_code}' --max-time 5 "$(authorize_url)" || true)"
  if [[ "$acode" == "302" ]]; then
    gate C-3 "/oauth/authorize reaches the login path (registered client + redirect_uri)" "added" PASS \
      "no-session GET -> 302 $(sed -n 's/^[Ll]ocation: //p' "$ahdr" | tr -d '\r' | head -1)"
  else
    gate C-3 "/oauth/authorize reaches the login path (registered client + redirect_uri)" "added" FAIL \
      "no-session GET -> $acode (expected a 302 to the login entry point)"
  fi

  # C-5 (added, LOAD-BEARING) — is the design's step-5 check LIVE on this
  # deployment at all?
  #
  # Every other gate can pass while the experiment still measures nothing:
  # C-1b proves the IdP serves discovery, C-1c proves an IdP config row
  # exists, C-3 proves /oauth/authorize reaches the login path — and the
  # run can STILL be vacuous if the RP never contacts the IdP. This gate
  # closes that hole by probing the one surface that would: a steady-state
  # /auth/oidc/login must 302 to the IdP's own authorize endpoint. If it
  # 400s while the IdP is demonstrably up (C-1b), the RP is not talking to
  # the IdP and no injection can change its behaviour.
  #
  # Deliberately NOT a FAIL. A FAIL would refuse to inject and the run
  # would produce no measurements at all — but "the RP never contacts the
  # IdP" is not an unverified stack, it is a FINDING about the stack, and
  # the design's other two checks (existing JWT, key rotation) remain
  # fully testable. So the gate records VACUOUS, the verdict carries it,
  # and the decisions log reports it as the headline rather than burying
  # a silent null result. See the decisions log D-numbered entry on this.
  local lcode lbody="$OUT_DIR/preflight-oidc-login.body"
  lcode="$(curl -s -o "$lbody" -w '%{http_code}' --max-time 5 \
    "$HTTP_BASE/auth/oidc/login?tenant_id=$TENANT_ID&idp=primary" || true)"
  if [[ "$lcode" == "302" ]]; then
    gate C-5 "New-login path actually contacts the IdP (design step 5 is live)" "added (LOAD-BEARING)" PASS \
      "GET /auth/oidc/login -> 302 to the IdP authorize endpoint"
  else
    gate C-5 "New-login path actually contacts the IdP (design step 5 is live)" "added (LOAD-BEARING)" VACUOUS \
      "GET /auth/oidc/login -> $lcode $(tr -d '\n' <"$lbody" | cut -c1-120) — the RP never reaches the IdP, so the design's new-login check cannot be exercised by ANY injection. Recorded, not fatal: the existing-JWT and key-rotation checks stay live."
  fi

  # C-4 (added, LOAD-BEARING) — the key-rotation cron must be observed
  # ticking before injection. "No rotation errors during the outage" is
  # vacuous if no rotation ever fires; the shipped default cadence is
  # annual, so this gate is what makes the design's third check real.
  log "  waiting up to 150s for the key-rotation cron to tick at least once..."
  local rot_start rot_deadline seen=0
  rot_start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  rot_deadline=$((SECONDS + 150))
  while ((SECONDS < rot_deadline)); do
    read -r seen _ < <(rotation_stats "$rot_start" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$OUT_DIR/preflight-rotation.log")
    ((seen == 0)) || break
    sleep 10
  done
  if ((seen > 0)); then
    gate C-4 "Key-rotation cron observed ticking BEFORE injection" "added (see run header)" PASS \
      "$seen rotation event(s) in ${ATLAS_KEY_ROTATION_INTERVAL:-unset} cadence window"
  else
    gate C-4 "Key-rotation cron observed ticking BEFORE injection" "added (see run header)" FAIL \
      "no audit_event=key_rotation line observed in 150s (cadence=${ATLAS_KEY_ROTATION_INTERVAL:-unset})"
  fi

  # H-1 (added, measurement) — harness floor, so a latency reading is
  # not misread as platform behaviour.
  # Taken from the host, against the same loopback endpoint the phase
  # probes use, so a per-tick latency number can be read against a known
  # floor instead of in the abstract.
  local floor
  floor="$(curl -s -o /dev/null -w '%{time_total}' --max-time 5 "$HTTP_BASE/health" || echo 0)"
  gate H-1 "Harness floor recorded" "added (measurement)" INFO \
    "a loopback /health round trip costs $(awk '{printf "%d", $1*1000}' <<<"$floor")ms through the same construct"

  ((PREFLIGHT_FAILED == 0)) || die "REFUSED: pre-execution checklist FAILED — not injecting into an unverified stack (slice 357: do NOT skip the checklist)" 1
  log "pre-execution checklist: all gates PASS"
}

# --- injection / recovery --------------------------------------------

# Arm A — the design's parenthetical mechanism: detach the IdP container
# from the network that reaches it. Removes the docker-DNS entry, so the
# RP's discovery call would fail at name resolution.
inject_detach() {
  log "INJECT: docker network disconnect $IDP_NETWORK ${DEX_CID:0:12} (design step 4)"
  docker network disconnect "$IDP_NETWORK" "$DEX_CID" >>"$RUN_LOG" 2>&1 ||
    die "could not detach the IdP container from $IDP_NETWORK"
}

recover_detach() {
  log "ROLLBACK: docker network connect $IDP_NETWORK ${DEX_CID:0:12} (design Rollback)"
  docker network connect --alias "$IDP_SERVICE" "$IDP_NETWORK" "$DEX_CID" >>"$RUN_LOG" 2>&1 ||
    die "could not re-attach the IdP container to $IDP_NETWORK"
}

# Arm B — the design's first-named mechanism, verbatim:
# `iptables -A OUTPUT -d <idp-ip> -j DROP`, installed inside atlas's own
# network namespace. DNS still resolves, so the RP's discovery call would
# fail on a CONNECT TIMEOUT instead — which is the failure shape the
# design's "missing timeout on the OIDC discovery refresh" abort
# criterion is actually about.
inject_blackhole() {
  log "INJECT: iptables -A OUTPUT -d $DEX_IP -j DROP, in atlas's network namespace"
  docker exec --privileged "$NETPROBE_NAME" iptables -A OUTPUT -d "$DEX_IP" -j DROP >>"$RUN_LOG" 2>&1 ||
    die "could not install the egress DROP rule"
  BLACKHOLE_IP="$DEX_IP"
  docker exec "$NETPROBE_NAME" iptables -S OUTPUT >>"$RUN_LOG" 2>&1 || true
}

recover_blackhole() {
  log "ROLLBACK: iptables -D OUTPUT -d $DEX_IP -j DROP"
  docker exec --privileged "$NETPROBE_NAME" iptables -D OUTPUT -d "$DEX_IP" -j DROP >>"$RUN_LOG" 2>&1 ||
    die "could not remove the egress DROP rule"
  BLACKHOLE_IP=""
}

# measure_recovery <phase> — the design's step 6 + its "new logins resume
# within 30s" expectation, measured rather than assumed. THREE clocks are
# kept apart on purpose, because they answer three different questions:
#
#   IDP_RECOVERED_S    when the IdP is reachable from atlas's netns again
#   AUTHZ_RECOVERED_S  when /oauth/authorize is back to its steady-state code
#   LOGIN_RECOVERED_S  when /auth/oidc/login — the surface that actually
#                      talks to the IdP — is back to its steady-state code
#
# Collapsing them would hide the case this experiment is likely to hit: a
# surface that never changed under the outage "recovers" at t+0s, which is
# not a recovery measurement at all. Reporting all three keeps a vacuous
# zero legible as vacuous instead of letting it read as a fast recovery.
measure_recovery() {
  local phase="$1"
  local t0=$SECONDS
  IDP_RECOVERED_S=""
  AUTHZ_RECOVERED_S=""
  LOGIN_RECOVERED_S=""
  local tsv="$OUT_DIR/$phase.tsv"
  : >"$tsv"
  local tick=0 acode lcode
  while ((SECONDS - t0 < RECOVERY_DEADLINE_SECONDS)); do
    probe_tick "$phase" "$tick" "$((SECONDS - t0))"
    if [[ -z "$IDP_RECOVERED_S" && "$LAST_IDP_CODE" == "200" ]]; then
      IDP_RECOVERED_S=$((SECONDS - t0))
      log "  IdP discovery reachable again at t+${IDP_RECOVERED_S}s"
    fi
    acode="$(awk -F'\t' '$4=="authorize"{c=$5} END{print c}' "$tsv")"
    lcode="$(awk -F'\t' '$4=="oidc_login"{c=$5} END{print c}' "$tsv")"
    if [[ -z "$AUTHZ_RECOVERED_S" && "$acode" == "$STEADY_AUTHORIZE_CODE" ]]; then
      AUTHZ_RECOVERED_S=$((SECONDS - t0))
    fi
    if [[ -z "$LOGIN_RECOVERED_S" && "$lcode" == "$STEADY_OIDC_LOGIN_CODE" ]]; then
      LOGIN_RECOVERED_S=$((SECONDS - t0))
      log "  new-login entry point back to its steady-state code at t+${LOGIN_RECOVERED_S}s"
    fi
    [[ -z "$IDP_RECOVERED_S" || -z "$AUTHZ_RECOVERED_S" || -z "$LOGIN_RECOVERED_S" ]] || break
    tick=$((tick + 1))
    sleep "$PROBE_INTERVAL_SECONDS"
  done
}

# --- arms -------------------------------------------------------------
# Arms A and B are the design's two named injection mechanisms (see the
# run header).
#
# Arm C was designed to defeat the RP's provider cache
# (internal/auth/oidc.Authenticator caches the discovered provider per
# issuer with no TTL and no invalidation, so a warm cache would stop
# /auth/oidc/login from ever re-contacting the IdP). Reading further up
# the call chain showed the cache is not even reached on this deployment:
# cmd/atlas/main.go wires `oidc.New(localModeIdpResolver{})`, whose
# ResolveIdp returns ErrUnknownIdp unconditionally, and BeginLogin
# resolves BEFORE it touches the cache. So the new-login path fails at
# resolution on every request, injected or not — see gate C-5.
#
# Arm C is kept, for two things it still measures that A and B cannot:
#
#   1. It distinguishes the two candidate explanations EMPIRICALLY rather
#      than by code reading alone. If the 400s were a warm-cache artifact
#      they would change under a cold cache; if they are the resolver
#      stub they will not. A run that only reads the source and asserts
#      the answer is weaker evidence than one that tests it.
#   2. It puts the design's central claim — "key verification is local;
#      no IdP roundtrip" — under a harder perturbation than the design
#      asked for: a JWT that outlived the process that minted it. Slice
#      356b found an in-memory credential store that did NOT survive a
#      restart, so "the keystore is on a volume" is not an assumption
#      worth making for free here.
ARMS="A B C"

arm_label() {
  case "$1" in
  A) echo "detach IdP container from the project network (design Variable, parenthetical mechanism) — provider cache WARM" ;;
  B) echo "iptables -A OUTPUT -d <idp-ip> -j DROP inside atlas's netns (design Variable, first-named mechanism) — provider cache WARM" ;;
  C) echo "detach IdP, then restart atlas — cold provider cache + a JWT that outlived the process that minted it" ;;
  esac
}

arm_prepare() {
  JWT_AFTER_PREPARE="n/a"
  case "$1" in
  A) : ;;
  # Arm B's DROP rule targets the IdP by address, and arm A's re-attach
  # can hand the container a different address, so this is re-resolved
  # here rather than cached from bring-up.
  B) resolve_dex_ip ;;
  C)
    log "arm C prepare: detaching the IdP, then restarting atlas for a cold provider cache"
    inject_detach
    compose restart atlas >>"$RUN_LOG" 2>&1 || die "arm C: could not restart atlas"
    local deadline=$((SECONDS + 240))
    until [[ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$HTTP_BASE/health" || true)" == "200" ]]; do
      ((SECONDS < deadline)) || die "arm C: atlas /health did not return to 200 within 240s of the restart"
      sleep 3
    done
    # The sidecar shared the OLD network namespace. A restarted container
    # gets a new one, so without re-creating the probe every
    # idp_discovery reading after this point would measure a dead
    # namespace and the arm's own witness would be worthless.
    start_netprobe
    # Does a JWT minted before the restart still authenticate? The design
    # claims key verification is local, and the keystore is a volume, so
    # it should. Measured rather than assumed, and recorded either way —
    # slice 356b found an in-memory credential store that did NOT survive
    # a restart, so "it's on a volume" is not a safe assumption here.
    JWT_AFTER_PREPARE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
      -H "Authorization: Bearer $JWT" "$HTTP_BASE/v1/me" || true)"
    log "arm C: JWT minted before the restart, on /v1/me after it -> $JWT_AFTER_PREPARE"
    ;;
  esac
}

arm_inject() {
  case "$1" in
  A) inject_detach ;;
  B) inject_blackhole ;;
  C)
    # Detaching IS this arm's injection, and it had to precede the
    # restart to keep the cache cold. Nothing further to inject.
    log "INJECT arm C: IdP already detached before the restart (see arm_prepare)"
    ;;
  esac
}

arm_recover() {
  case "$1" in
  A | C) recover_detach ;;
  B) recover_blackhole ;;
  esac
}

# run_arm <arm> — one complete prepare → inject → hold → recover →
# observe cycle. Every arm shares the single steady-state baseline
# captured before any injection, the same probes, and the same hold
# duration; only the injection mechanism (and, for arm C, the cache
# state) differs. Results land in arm-<arm>.meta so the report reads
# them back from disk rather than relying on bash 4 associative arrays
# (this platform's /bin/bash is 3.2).
run_arm() {
  local arm="$1"
  local ip="injection_$arm" rp="recovery_$arm" pp="post_recovery_$arm"
  local restarts_before restarts_after inject_at recover_at inject_end rot_ok rot_err

  log "=============================================================="
  log "arm $arm — $(arm_label "$arm")"
  log "=============================================================="
  restarts_before="$(docker inspect -f '{{.RestartCount}}' "$ATLAS_CID")"
  arm_prepare "$arm"

  inject_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  arm_inject "$arm"
  run_phase "$ip" "$INJECT_SECONDS" abortable
  inject_end="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  read -r rot_ok rot_err < <(rotation_stats "$inject_at" "$inject_end" "$OUT_DIR/rotation-$arm.log")
  [[ -z "$PHASE_ABORTED" ]] || log "arm $arm injection ended early: $PHASE_ABORTED"

  recover_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  arm_recover "$arm"
  measure_recovery "$rp"
  run_phase "$pp" "$POST_RECOVERY_SECONDS" plain
  restarts_after="$(docker inspect -f '{{.RestartCount}}' "$ATLAS_CID")"

  {
    echo "arm=$arm"
    echo "label=$(arm_label "$arm")"
    echo "inject_at=$inject_at"
    echo "recover_at=$recover_at"
    echo "aborted=${PHASE_ABORTED:-}"
    echo "rot_ok=$rot_ok"
    echo "rot_err=$rot_err"
    echo "restarts_before=$restarts_before"
    echo "restarts_after=$restarts_after"
    echo "idp_recovered_s=${IDP_RECOVERED_S:-}"
    echo "authz_recovered_s=${AUTHZ_RECOVERED_S:-}"
    echo "login_recovered_s=${LOGIN_RECOVERED_S:-}"
    echo "jwt_after_prepare=${JWT_AFTER_PREPARE:-n/a}"
  } >"$OUT_DIR/arm-$arm.meta"
  log "arm $arm complete"
}

# --- reporting --------------------------------------------------------

summarize() {
  local phase="$1"
  local tsv="$OUT_DIR/$phase.tsv"
  [[ -f "$tsv" ]] || return 0
  awk -F'\t' -v phase="$phase" '
    { n[$4]++; codes[$4"|"$5]++; sum[$4]+=$6; if ($6+0 > max[$4]) max[$4]=$6 }
    END {
      for (p in n) {
        line = ""
        for (k in codes) { split(k, a, "|"); if (a[1]==p) line = line a[2] "x" codes[k] " " }
        printf "%s\t%s\t%d\t%s\t%d\t%d\n", phase, p, n[p], line, sum[p]/n[p], max[p]
      }
    }' "$tsv" | sort -k2,2
}

# code_hist <phase> <probe> — the status-code histogram for one probe in
# one phase, e.g. "200x60". The whole experiment's evidence is code
# distributions per phase, so this is the report's basic unit.
code_hist() {
  local tsv="$OUT_DIR/$1.tsv"
  [[ -f "$tsv" ]] || {
    echo "(no data)"
    return 0
  }
  awk -F'\t' -v p="$2" '$4==p{print $5}' "$tsv" | sort | uniq -c |
    awk '{printf "%sx%s ", $2, $1}'
  echo
}

meta_get() { sed -n "s/^$2=//p" "$OUT_DIR/arm-$1.meta"; }

arm_report() {
  local arm="$1"
  local ip="injection_$arm" rp="recovery_$arm"
  local jwt_bad health_bad aborted idp_s authz_s login_s

  jwt_bad="$(awk -F'\t' '$4=="existing_jwt" && $5!="200"' "$OUT_DIR/$ip.tsv" | wc -l | tr -d ' ')"
  health_bad="$(awk -F'\t' '$4=="health" && $5!="200"' "$OUT_DIR/$ip.tsv" | wc -l | tr -d ' ')"
  aborted="$(meta_get "$arm" aborted)"
  idp_s="$(meta_get "$arm" idp_recovered_s)"
  authz_s="$(meta_get "$arm" authz_recovered_s)"
  login_s="$(meta_get "$arm" login_recovered_s)"

  echo
  echo "=============================================================="
  echo "ARM $arm — $(meta_get "$arm" label)"
  echo "=============================================================="
  echo "injected at / rolled back at   : $(meta_get "$arm" inject_at) / $(meta_get "$arm" recover_at)"
  echo "injection ended early          : ${aborted:-no (ran the full ${INJECT_SECONDS}s)}"
  if [[ "$arm" == "C" ]]; then
    echo "pre-restart JWT after restart  : $(meta_get "$arm" jwt_after_prepare) on /v1/me"
  fi
  echo
  echo "-- did the injection actually reach the variable? --"
  echo "IdP discovery, steady state    : $(code_hist steady_state idp_discovery)"
  echo "IdP discovery, this injection  : $(code_hist "$ip" idp_discovery)"
  echo "   (a run whose discovery probe does not flip here is not evidence of anything)"
  echo
  echo "-- the design's abort criteria --"
  printf 'existing JWT verification failed : %s tick(s) -> %s\n' "$jwt_bad" \
    "$([[ "$jwt_bad" == "0" ]] && echo "not tripped" || echo "TRIPPED (falsifies the design claim)")"
  printf 'atlas unhealthy / crashed        : %s tick(s) -> %s\n' "$health_bad" \
    "$([[ "$health_bad" == "0" ]] && echo "not tripped" || echo "TRIPPED")"
  echo "atlas RestartCount before -> after: $(meta_get "$arm" restarts_before) -> $(meta_get "$arm" restarts_after)"
  echo
  echo "-- the design's three during-outage checks --"
  echo "1. existing JWT on a protected endpoint"
  echo "   design expects : continues to work for the TTL remainder"
  echo "   measured       : $(code_hist "$ip" existing_jwt)"
  echo "2. new login attempt"
  echo "   design expects : 503 {\"error\":\"auth_provider_unavailable\",\"retry_after\":30}"
  echo "   /oauth/authorize  measured : $(code_hist "$ip" authorize)"
  echo "   /auth/oidc/login  measured : $(code_hist "$ip" oidc_login)"
  echo "   login-entry body, steady state : $(head -c 400 "$OUT_DIR/last-oidc-login-body.steady_state.txt" 2>/dev/null)"
  echo "   login-entry body, injection    : $(head -c 400 "$OUT_DIR/last-oidc-login-body.$ip.txt" 2>/dev/null)"
  echo "3. atlas-issued JWT key-rotation"
  echo "   steady state : $STEADY_ROT_OK rotation(s), $STEADY_ROT_ERR error line(s)"
  echo "   injection    : $(meta_get "$arm" rot_ok) rotation(s), $(meta_get "$arm" rot_err) error line(s)"
  echo
  echo "-- recovery (design step 6; design expects new logins within 30s) --"
  echo "IdP reachable again              : ${idp_s:-never within ${RECOVERY_DEADLINE_SECONDS}s}s"
  echo "/oauth/authorize back to steady  : ${authz_s:-never within ${RECOVERY_DEADLINE_SECONDS}s}s"
  echo "/auth/oidc/login back to steady  : ${login_s:-never within ${RECOVERY_DEADLINE_SECONDS}s}s"
  echo "post-recovery authorize          : $(code_hist "post_recovery_$arm" authorize)"
  echo "post-recovery oidc_login         : $(code_hist "post_recovery_$arm" oidc_login)"
  echo
  echo "-- is this arm's falsification check live, or vacuous? --"
  local inj_login
  inj_login="$(awk -F'\t' '$4=="oidc_login"{print $5}' "$OUT_DIR/$ip.tsv" | sort | uniq -c | sort -rn | head -1 | awk '{print $2}')"
  echo "steady-state login-entry code : $STEADY_OIDC_LOGIN_CODE"
  echo "injection    login-entry code : $inj_login"
  if [[ "$STEADY_OIDC_LOGIN_CODE" == "$inj_login" ]]; then
    echo "-> IDENTICAL. The new-login surface did not observe the outage at all."
    echo "   Do NOT read this as graceful degradation without first checking whether"
    echo "   the RP ever issues a discovery call in this cache state. See the"
    echo "   decisions log; this is exactly why arm C exists."
  else
    echo "-> the new-login surface changed under the outage. Read the codes and the"
    echo "   body above against the design's expected 503 + structured body."
  fi
}

report() {
  local v="$OUT_DIR/verdict.txt"
  local arm
  {
    echo "slice 335 Experiment 4 — OIDC IdP unavailable — run $RUN_TAG"
    echo "executed by slice 357a against LOCAL docker-compose ONLY"
    echo "steady state ${STEADY_SECONDS}s captured BEFORE any injection;"
    echo "each arm holds ${INJECT_SECONDS}s at a ${PROBE_INTERVAL_SECONDS}s probe cadence"
    echo
    echo "== phase / probe / ticks / status-code histogram / mean ms / max ms =="
    printf 'phase\tprobe\tticks\tcodes\tmean_ms\tmax_ms\n'
    summarize steady_state
    for arm in $ARMS; do
      summarize "injection_$arm"
      summarize "recovery_$arm"
      summarize "post_recovery_$arm"
    done
    echo
    echo "== STEADY STATE (before any injection) =="
    echo "/oauth/authorize            : $(code_hist steady_state authorize)"
    echo "/auth/oidc/login            : $(code_hist steady_state oidc_login)"
    echo "existing JWT on /v1/me      : $(code_hist steady_state existing_jwt)"
    echo "IdP discovery (atlas netns) : $(code_hist steady_state idp_discovery)"
    echo "atlas /health               : $(code_hist steady_state health)"
    echo "key rotations / error lines : $STEADY_ROT_OK / $STEADY_ROT_ERR"
    for arm in $ARMS; do arm_report "$arm"; done
  } >"$v"
  cat "$v"
}

# cleanup — never leave the stack perturbed, whichever way the run ends.
# Both injections are undone unconditionally and best-effort: an
# `iptables` rule left in atlas's namespace or an IdP left off the network
# would silently poison every later run on this machine.
cleanup() {
  if [[ -n "${BLACKHOLE_IP:-}" && -n "${NETPROBE_NAME:-}" ]]; then
    docker exec --privileged "$NETPROBE_NAME" \
      iptables -D OUTPUT -d "$BLACKHOLE_IP" -j DROP >/dev/null 2>&1 || true
  fi
  stop_netprobe
  if [[ -n "${DEX_CID:-}" && -n "${IDP_NETWORK:-}" ]]; then
    docker network connect --alias "$IDP_SERVICE" "$IDP_NETWORK" "$DEX_CID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

main() {
  log "run-exp4 start — tag=$RUN_TAG out=$OUT_DIR"
  log "compose: $COMPOSE_FILE + $OVERLAY_FILE"
  command -v jq >/dev/null || die "jq is required"

  bring_up
  start_netprobe
  # mint_jwt first: ensure_idp_config authenticates with the admin JWT it
  # produces (see that function's note).
  mint_jwt
  ensure_idp_config
  ensure_oauth_client
  preflight

  # --- steady state, BEFORE any injection (design step 3) ---
  local t_steady_start t_steady_end
  t_steady_start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  run_phase steady_state "$STEADY_SECONDS" plain
  t_steady_end="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  read -r STEADY_ROT_OK STEADY_ROT_ERR < <(rotation_stats "$t_steady_start" "$t_steady_end" "$OUT_DIR/rotation-steady.log")
  STEADY_AUTHORIZE_CODE="$(awk -F'\t' '$4=="authorize"{print $5}' "$OUT_DIR/steady_state.tsv" | sort | uniq -c | sort -rn | head -1 | awk '{print $2}')"
  STEADY_OIDC_LOGIN_CODE="$(awk -F'\t' '$4=="oidc_login"{print $5}' "$OUT_DIR/steady_state.tsv" | sort | uniq -c | sort -rn | head -1 | awk '{print $2}')"
  log "steady state captured: rotations=$STEADY_ROT_OK errors=$STEADY_ROT_ERR authorize=$STEADY_AUTHORIZE_CODE oidc_login=$STEADY_OIDC_LOGIN_CODE"

  # --- the arms (design steps 4, 5, 6, once per injection mechanism) ---
  local arm
  for arm in $ARMS; do
    run_arm "$arm"
  done

  report
  log "run-exp4 done — artifacts in $OUT_DIR"
}

main "$@"
