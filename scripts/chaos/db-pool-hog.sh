#!/usr/bin/env bash
#
# db-pool-hog.sh — connection-storm injection primitive for slice 335
# Experiment 1 (evidence-ledger DB connection-pool exhaustion), executed by
# slice 354.
#
# Opens N Postgres connections against the LOCAL docker-compose Postgres
# container and holds them for a bounded duration, then releases them. Each
# held connection is a `psql -c "SELECT pg_sleep(<hold>)"` process, so the
# storm self-releases when the sleep expires even if this script is killed.
#
# SCOPE BOUNDARY (slice 335 P0-335-2 / slice 354 P0-1) — LOAD-BEARING.
# This script cannot target a hosted or edge deployment by construction:
#
#   * it accepts NO host, NO port, NO DSN, and NO password — the only
#     connection knob is a docker container NAME;
#   * it refuses to run unless the active docker context speaks to a LOCAL
#     daemon (unix:// or npipe://), so a remote-context `docker exec` cannot
#     smuggle the storm onto another machine;
#   * it refuses any container whose `com.docker.compose.project` label is
#     not the expected local compose project.
#
# Auth posture: connections are opened from INSIDE the container as the
# container's `postgres` OS user over the local socket. No credential is
# read, passed on a command line, or written to disk by this script.
#
# SAFETY: the hold duration is hard-capped (see MAX_HOLD_SECONDS) and the
# connection count is hard-capped at `max_connections - headroom`, so the
# stack always self-recovers and the platform keeps a reserved slot budget.
#
# Environment:
#   ATLAS_CHAOS_PG_CONTAINER   postgres container (default: security-atlas-postgres-1)
#   ATLAS_CHAOS_COMPOSE_PROJECT expected compose project label (default: security-atlas)
#   ATLAS_CHAOS_PG_DB          database name (default: security_atlas)
#   ATLAS_CHAOS_PG_USER        in-container OS/DB user (default: postgres)
#
# Usage:
#   scripts/chaos/db-pool-hog.sh [--connections N] [--hold-seconds S]
#                                [--headroom H] [--dry-run]
#
# Exit: 0 storm ran and released, 1 refused (guard tripped), 2 env error.

set -eu

MAX_HOLD_SECONDS=900
DEFAULT_HOLD_SECONDS=300
DEFAULT_HEADROOM=8

CONTAINER="${ATLAS_CHAOS_PG_CONTAINER:-security-atlas-postgres-1}"
PROJECT="${ATLAS_CHAOS_COMPOSE_PROJECT:-security-atlas}"
PGDB="${ATLAS_CHAOS_PG_DB:-security_atlas}"
PGUSER="${ATLAS_CHAOS_PG_USER:-postgres}"

CONNECTIONS=""
HOLD_SECONDS="$DEFAULT_HOLD_SECONDS"
HEADROOM="$DEFAULT_HEADROOM"
DRY_RUN=0

die() {
  echo "db-pool-hog: $1" >&2
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
  --connections)
    CONNECTIONS="${2:-}"
    shift 2
    ;;
  --hold-seconds)
    HOLD_SECONDS="${2:-}"
    shift 2
    ;;
  --headroom)
    HEADROOM="${2:-}"
    shift 2
    ;;
  --dry-run)
    DRY_RUN=1
    shift
    ;;
  -h | --help)
    sed -n '2,45p' "$0"
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

# ---------------------------------------------------------------------------
# Input validation. Every value that reaches the in-container shell below is
# an integer or a name matched against a strict allowlist pattern — nothing
# from argv is interpolated into SQL or into a shell word unquoted.
# ---------------------------------------------------------------------------
is_uint "$HOLD_SECONDS" || die "--hold-seconds must be a non-negative integer"
is_uint "$HEADROOM" || die "--headroom must be a non-negative integer"
if [ -n "$CONNECTIONS" ]; then
  is_uint "$CONNECTIONS" || die "--connections must be a non-negative integer"
fi

[ "$HOLD_SECONDS" -gt 0 ] || die "--hold-seconds must be > 0"
[ "$HOLD_SECONDS" -le "$MAX_HOLD_SECONDS" ] ||
  die "--hold-seconds $HOLD_SECONDS exceeds the ${MAX_HOLD_SECONDS}s cap; the storm must self-release" 1

case "$CONTAINER" in
*[!A-Za-z0-9_.-]*) die "container name has characters outside [A-Za-z0-9_.-]: $CONTAINER" 1 ;;
esac
case "$PGDB$PGUSER" in
*[!A-Za-z0-9_]*) die "db/user name has characters outside [A-Za-z0-9_]" 1 ;;
esac

command -v docker >/dev/null 2>&1 || die "docker not found on PATH"

# ---------------------------------------------------------------------------
# Guard 1 — the docker daemon must be LOCAL. A remote context would let this
# storm land on a machine that is not this one, which the slice 335 blast
# radius forbids.
# ---------------------------------------------------------------------------
DOCKER_HOST_ENDPOINT="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || echo '')"
case "$DOCKER_HOST_ENDPOINT" in
unix://* | npipe://*) : ;;
'') die "could not read the docker context endpoint; refusing to inject" 1 ;;
*) die "active docker context is remote ($DOCKER_HOST_ENDPOINT); this experiment is local-compose ONLY" 1 ;;
esac

# ---------------------------------------------------------------------------
# Guard 2 — the target container must be running and must belong to the
# expected local compose project.
# ---------------------------------------------------------------------------
RUNNING="$(docker inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null || echo 'false')"
[ "$RUNNING" = "true" ] || die "container '$CONTAINER' is not running" 1

LABEL="$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$CONTAINER" 2>/dev/null || echo '')"
[ "$LABEL" = "$PROJECT" ] ||
  die "container '$CONTAINER' has compose project '$LABEL', expected '$PROJECT'; refusing to inject" 1

# ---------------------------------------------------------------------------
# Derive the storm size from the server's own max_connections so the platform
# always keeps `headroom` slots. Slice 335 Experiment 1 Method step 3:
# "N = configured max_connections minus reserved-for-platform headroom".
# ---------------------------------------------------------------------------
MAXCONN="$(docker exec "$CONTAINER" psql -U "$PGUSER" -d "$PGDB" -Atc 'SHOW max_connections;' 2>/dev/null || echo '')"
is_uint "$MAXCONN" || die "could not read max_connections from '$CONTAINER'"

RESERVED="$(docker exec "$CONTAINER" psql -U "$PGUSER" -d "$PGDB" -Atc 'SHOW superuser_reserved_connections;' 2>/dev/null || echo '0')"
is_uint "$RESERVED" || RESERVED=0

CEILING=$((MAXCONN - RESERVED - HEADROOM))
[ "$CEILING" -gt 0 ] || die "computed connection ceiling is $CEILING; widen --headroom or raise max_connections" 1

if [ -z "$CONNECTIONS" ]; then
  CONNECTIONS="$CEILING"
elif [ "$CONNECTIONS" -gt "$CEILING" ]; then
  echo "db-pool-hog: clamping --connections $CONNECTIONS to ceiling $CEILING (max_connections=$MAXCONN reserved=$RESERVED headroom=$HEADROOM)" >&2
  CONNECTIONS="$CEILING"
fi
[ "$CONNECTIONS" -gt 0 ] || die "connection count resolved to 0" 1

echo "db-pool-hog: container=$CONTAINER project=$PROJECT db=$PGDB"
echo "db-pool-hog: max_connections=$MAXCONN superuser_reserved=$RESERVED headroom=$HEADROOM"
echo "db-pool-hog: opening $CONNECTIONS connections, holding ${HOLD_SECONDS}s"

if [ "$DRY_RUN" -eq 1 ]; then
  echo "db-pool-hog: --dry-run set; no connections opened"
  exit 0
fi

# ---------------------------------------------------------------------------
# Rollback path. The in-container sleeps expire on their own, but an operator
# Ctrl-C should release immediately (slice 335 Experiment 1 Rollback: "kill
# the connection-hogger script process").
# ---------------------------------------------------------------------------
release() {
  echo "db-pool-hog: releasing connections" >&2
  docker exec "$CONTAINER" pkill -f 'SELECT pg_sleep' >/dev/null 2>&1 || true
}
trap 'release; exit 130' INT TERM

STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "db-pool-hog: storm start $STARTED_AT"

# `-i` is deliberately omitted: the inner script is passed as an argument, not
# on stdin, so this runs cleanly from a non-interactive harness. CONNECTIONS
# and HOLD_SECONDS are validated integers (is_uint above).
docker exec "$CONTAINER" sh -c '
  n="$1"; hold="$2"; user="$3"; db="$4"
  i=0
  while [ "$i" -lt "$n" ]; do
    psql -U "$user" -d "$db" -Atc "SELECT pg_sleep($hold);" >/dev/null 2>&1 &
    i=$((i + 1))
  done
  wait
' sh "$CONNECTIONS" "$HOLD_SECONDS" "$PGUSER" "$PGDB" || true

release
echo "db-pool-hog: storm end $(date -u +%Y-%m-%dT%H:%M:%SZ) (started $STARTED_AT)"
