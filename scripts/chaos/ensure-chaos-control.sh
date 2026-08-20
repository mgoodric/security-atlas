#!/usr/bin/env bash
#
# ensure-chaos-control.sh — test-fixture helper for slice 335 Experiment 3.
#
# Experiment 3's write probe (`POST /v1/evidence:push`) carries a
# `control_id`. `evidence_records` has a composite FK on
# (tenant_id, control_id), so the control MUST belong to the SAME tenant
# the chaos bearer token is scoped to. Get that wrong and the experiment
# looks like it is working — the push returns 200, because the NATS-backed
# ingest path acks onto the buffer BEFORE the ledger write — while every
# record silently fails its insert and redelivers forever. The ledger then
# reads 0 rows for the whole run, and the design's "snapshot the row count
# BEFORE, verify identical AFTER" check passes VACUOUSLY: 0 == 0.
#
# That is not hypothetical. It is what happened on the first OE-384
# execution attempt, whose write probe used `select id from controls
# limit 1` — a row belonging to the catalog tenant
# (00000000-0000-0000-0000-000000000000), not the bearer's tenant. This
# script exists so the mistake cannot recur silently.
#
# It prints a control_id on stdout that is guaranteed to belong to
# $TENANT_ID, creating a minimal chaos-fixture control if none exists.
# Idempotent: a second run reuses the row it created the first time.
#
# LOCAL ONLY. It talks to the postgres container of the named compose
# project and nothing else.
#
# Usage:
#   scripts/chaos/ensure-chaos-control.sh [--tenant <uuid>] [--compose-file <path>]
#
# Exit: 0 with the control_id on stdout, 2 on environment error.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker/docker-compose.yml"
ENV_FILE="$REPO_ROOT/deploy/docker/.env"
TENANT_ID="00000000-0000-4000-8000-000000000001"
# Deterministic so reruns converge on one fixture row rather than
# accumulating a new control per chaos run.
CONTROL_ID="cccccccc-0000-4000-8000-000000000384"

die() {
  echo "ensure-chaos-control: $1" >&2
  exit "${2:-2}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
  --tenant)
    TENANT_ID="${2:-}"
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

[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ "$TENANT_ID" =~ ^[0-9a-fA-F-]+$ ]] || die "--tenant is not a bare UUID"
command -v docker >/dev/null 2>&1 || die "docker not on PATH"

compose() {
  if [[ -f "$ENV_FILE" ]]; then
    docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
  else
    docker compose -f "$COMPOSE_FILE" "$@"
  fi
}

PG_CID="$(compose ps -q postgres || true)"
[[ -n "$PG_CID" ]] || die "no postgres container in the compose project at $COMPOSE_FILE"

psql_q() {
  docker exec "$PG_CID" psql -U postgres -d security_atlas -Atc "$1"
}

# Prefer a control the tenant already owns; only synthesize when the
# tenant genuinely has none.
existing="$(psql_q "select id from controls where tenant_id = '$TENANT_ID' limit 1;")"
if [[ -n "$existing" ]]; then
  echo "$existing"
  exit 0
fi

psql_q "insert into controls (
          id, tenant_id, title, description, control_family,
          implementation_type, owner_role, lifecycle_state,
          applicability_expr, bundle_id
        ) values (
          '$CONTROL_ID', '$TENANT_ID',
          'Chaos fixture control (slice 335 Experiment 3)',
          'Synthesized by scripts/chaos/ensure-chaos-control.sh so the Experiment 3 write probe has a tenant-scoped control_id. Not a real control.',
          'chaos', 'manual_attested', 'security', 'active', 'true', 'chaos-fixture'
        ) on conflict (id) do nothing;" >/dev/null

confirmed="$(psql_q "select id from controls where id = '$CONTROL_ID' and tenant_id = '$TENANT_ID';")"
[[ -n "$confirmed" ]] || die "failed to create the chaos fixture control"
echo "$confirmed"
