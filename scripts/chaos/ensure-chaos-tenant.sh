#!/usr/bin/env bash
#
# ensure-chaos-tenant.sh — fixture helper for slice 335 Experiment 5,
# executed by slice 356b.
#
# The design's pre-execution checklist requires a FRESH TENANT SCOPE so
# prior runs do not pollute the ledger count. This script provisions one
# end to end and makes it idempotent, so a rerun converges on the same
# fixture rather than accumulating tenants:
#
#   1. a `tenants` row
#   2. a `controls` row OWNED BY THAT TENANT
#   3. a credential-store bearer scoped to that tenant
#
# Step 2 is not optional bookkeeping. `evidence_records` carries a
# composite FK on (tenant_id, control_id). A control belonging to another
# tenant still yields a 200 receipt — the ingest path acks at NATS
# stream-commit, before the ledger write (`streambuf.go`, invariant #2) —
# while every insert fails its FK and redelivers forever. The ledger then
# reads zero rows and a count-based falsification check passes vacuously.
# Slice 356a hit exactly that trap; see its decisions log D2. This script
# is the guard.
#
# Step 3 needs an admin bearer. The gRPC ingest surface authenticates
# against the credential store, NOT against an OAuth JWT: a JWT minted by
# /v1/test/issue-jwt is rejected there with `Unauthenticated`. The
# bootstrap fixed-token admin credential (ATLAS_BOOTSTRAP_TOKEN, minted at
# boot by cmd/atlas/main.go) is what can issue tenant credentials.
#
# LOCAL ONLY. The postgres container is resolved from the named
# docker-compose project, and the gRPC endpoint must be a loopback
# literal. Neither can be pointed at atlas-edge or a hosted instance.
#
# SECRETS. The issued bearer is written to --bearer-out with mode 0600 and
# is never printed on stdout or passed on any argv (argv is world-readable
# via `ps` on this platform). The admin bootstrap token is read from the
# env-file, never from argv.
#
# Usage:
#   scripts/chaos/ensure-chaos-tenant.sh \
#     --tenant <uuid> --control <uuid> --label "steady state" \
#     --bearer-out /path/to/bearer --atlas-cli /path/to/atlas-cli
#
# Exit: 0 provisioned (tenant_id and control_id echoed on stdout),
#       1 refused (scope guard tripped), 2 environment error.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/deploy/docker/docker-compose.yml"
ENV_FILE="$REPO_ROOT/deploy/docker/.env"
GRPC_ENDPOINT="127.0.0.1:50051"
TENANT_ID=""
CONTROL_ID=""
LABEL="slice 335 Experiment 5 chaos fixture"
BEARER_OUT=""
ATLAS_CLI=""

die() {
  echo "ensure-chaos-tenant: $1" >&2
  exit "${2:-2}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
  --tenant)
    TENANT_ID="${2:-}"
    shift 2
    ;;
  --control)
    CONTROL_ID="${2:-}"
    shift 2
    ;;
  --label)
    LABEL="${2:-}"
    shift 2
    ;;
  --bearer-out)
    BEARER_OUT="${2:-}"
    shift 2
    ;;
  --atlas-cli)
    ATLAS_CLI="${2:-}"
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

[[ -n "$TENANT_ID" ]] || die "--tenant is required"
[[ -n "$CONTROL_ID" ]] || die "--control is required"
[[ -n "$BEARER_OUT" ]] || die "--bearer-out is required"
[[ -n "$ATLAS_CLI" ]] || die "--atlas-cli is required"
[[ -x "$ATLAS_CLI" ]] || die "atlas-cli not executable: $ATLAS_CLI"
[[ -f "$COMPOSE_FILE" ]] || die "compose file not found: $COMPOSE_FILE"
[[ -f "$ENV_FILE" ]] || die "env file not found: $ENV_FILE"
command -v docker >/dev/null 2>&1 || die "docker not on PATH"

# UUID-shaped, and single-quote free — these values are interpolated into
# SQL below, so the shape check is the injection guard.
uuid_re='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
[[ "$TENANT_ID" =~ $uuid_re ]] || die "--tenant is not a bare UUID"
[[ "$CONTROL_ID" =~ $uuid_re ]] || die "--control is not a bare UUID"
[[ "$LABEL" != *"'"* ]] || die "--label must not contain a single quote"

# --- scope guard: loopback gRPC endpoint -------------------------------
_host="${GRPC_ENDPOINT%:*}"
case "$_host" in
127.0.0.1 | localhost | ::1 | '[::1]') ;;
*)
  echo "ensure-chaos-tenant: REFUSED — endpoint host '$_host' is not loopback." >&2
  echo "  Slice 335 restricts every experiment to the local docker-compose" >&2
  echo "  stack (P0-335-2 / slice 356 P0-1). Refusing to proceed." >&2
  exit 1
  ;;
esac

compose() {
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

PG_CID="$(compose ps -q postgres || true)"
[[ -n "$PG_CID" ]] || die "no postgres container in the compose project at $COMPOSE_FILE"

psql_q() {
  docker exec "$PG_CID" psql -U postgres -d security_atlas -Atc "$1"
}

# --- 1 + 2: tenant and tenant-owned control ----------------------------
# `tenants` carries a unique index on BOTH `slug` and `lower(name)`, so both
# must be derived from the WHOLE tenant id. A prefix collides: every fixture
# tenant in this experiment shares the leading `dddddddd` block, and
# `on conflict (id)` absorbs neither a slug nor a name conflict — the insert
# fails on the second arm.
#
# The name in particular must NOT be derived from --label alone. Labels
# repeat across runs by design (every run has a `steady_state` arm), so a
# label-only name collides with the PREVIOUS run's tenant under
# `idx_tenants_lower_name` even though the ids differ, and the fixture dies
# before provisioning. Observed on this stack: a prior run's
# "Chaos fixture — preflight checklist" row blocked the next run's.
TENANT_SUFFIX="$(printf '%s' "$TENANT_ID" | tr -d '-')"
TENANT_SLUG="chaos-356b-$TENANT_SUFFIX"
psql_q "insert into tenants (id, name, slug)
        values ('$TENANT_ID', 'Chaos fixture — $LABEL — $TENANT_SUFFIX', '$TENANT_SLUG')
        on conflict (id) do nothing;" >/dev/null

psql_q "insert into controls (
          id, tenant_id, title, description, control_family,
          implementation_type, owner_role, lifecycle_state,
          applicability_expr, bundle_id
        ) values (
          '$CONTROL_ID', '$TENANT_ID',
          'Chaos fixture control ($LABEL)',
          'Synthesized by scripts/chaos/ensure-chaos-tenant.sh so the slice 335 Experiment 5 push driver has a tenant-scoped control_id. Not a real control.',
          'chaos', 'manual_attested', 'security', 'active', 'true', 'chaos-fixture'
        ) on conflict (id) do nothing;" >/dev/null

confirmed="$(psql_q "select id from controls where id = '$CONTROL_ID' and tenant_id = '$TENANT_ID';")"
[[ -n "$confirmed" ]] || die "failed to provision the fixture control for tenant $TENANT_ID"

# The whole point of the fixture is an unpolluted count. Refuse a tenant
# that already holds ledger rows rather than silently reporting a total
# that mixes runs.
existing_rows="$(psql_q "select count(*) from evidence_records where tenant_id = '$TENANT_ID';")"
if [[ "$existing_rows" != "0" ]]; then
  die "tenant $TENANT_ID already holds $existing_rows evidence_records rows — not a fresh scope. Pick a new --tenant."
fi

# --- 3: tenant-scoped credential ---------------------------------------
# Sourced from the env-file, never argv. `set -a` is scoped to a subshell
# read so the rest of this script does not inherit the whole env-file.
ADMIN_TOKEN="$(
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
  printf '%s' "${ATLAS_BOOTSTRAP_TOKEN:-}"
)"
[[ -n "$ADMIN_TOKEN" ]] || die "ATLAS_BOOTSTRAP_TOKEN not set in $ENV_FILE — cannot issue a tenant credential"

issue_json="$(
  SECURITY_ATLAS_ENDPOINT="$GRPC_ENDPOINT" SECURITY_ATLAS_TOKEN="$ADMIN_TOKEN" \
    "$ATLAS_CLI" credentials issue --insecure --tenant "$TENANT_ID" --ttl 0 2>&1
)" || die "credentials issue failed: $issue_json"

bearer="$(printf '%s' "$issue_json" | sed -n 's/.*"bearerToken"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
[[ -n "$bearer" ]] || die "could not parse bearerToken out of the issue response"

umask 077
printf '%s' "$bearer" >"$BEARER_OUT"
chmod 600 "$BEARER_OUT"

echo "tenant_id=$TENANT_ID"
echo "control_id=$CONTROL_ID"
echo "bearer_file=$BEARER_OUT"
