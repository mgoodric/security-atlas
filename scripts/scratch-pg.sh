#!/usr/bin/env bash
# security-atlas — throwaway Postgres cluster for local integration tests
# (security-atlas#2033).
#
# WHY THIS EXISTS. Concurrent agents each hand-rolled a scratch-cluster
# script on a fixed port (55432). When a second job's `pg_ctl start`
# failed because the port was taken, its script carried on and ran
# DROP DATABASE / CREATE DATABASE against whichever server was
# listening — another job's cluster. That happened twice
# (atlas-1786 vs atlas-1912 on 2026-11-13, atlas-1765 vs atlas-1780 on
# 2026-11-14). This script is the one supported way to stand up a
# scratch cluster, and it fails closed:
#
#   * `initdb` or `pg_ctl start` failing exits non-zero before any SQL.
#   * Every destructive statement runs in a psql session that first
#     checks `current_setting('data_directory')` equals this script's
#     own data directory, and stops if it does not. A foreign server on
#     the port is refused and nothing is dropped.
#   * The port is chosen per data directory and skipped if already in
#     use, never a fixed 55432.
#
# Usage:
#   scripts/scratch-pg.sh up   [DIR]  start (or reuse) the cluster, recreate
#                                     the `atlas` DB, apply roles + migrations,
#                                     print DATABASE_URL / DATABASE_URL_APP
#   scripts/scratch-pg.sh env  [DIR]  print the exports for a running cluster
#   scripts/scratch-pg.sh down [DIR]  stop the cluster and delete DIR
#
# DIR defaults to <repo>/.scratch-pg (gitignored), so each worktree gets
# its own cluster. SCRATCH_PG_PORT forces a port (the self-test uses it);
# if that port is busy the script refuses rather than picking another.
#
# Needs Postgres 16 client + server binaries on PATH (initdb, pg_ctl,
# psql), e.g. `brew install postgresql@16`.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
DB=atlas

die() {
    echo "scratch-pg: $*" >&2
    exit 1
}
log() { echo "scratch-pg: $*" >&2; }

cmd="${1:-}"
DIR="${2:-$REPO_ROOT/.scratch-pg}"
mkdir -p "$DIR"
DIR="$(cd "$DIR" && pwd -P)"
PGDATA_DIR="$DIR/pgdata"
PORT_FILE="$DIR/port"

port_in_use() {
    # bash /dev/tcp: the connect succeeds only if something listens.
    (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

pick_port() {
    if [ -n "${SCRATCH_PG_PORT:-}" ]; then
        # SCRATCH_PG_NO_PROBE=1 skips this probe so the self-test can
        # reproduce the race where the port is taken after we check it.
        [ "${SCRATCH_PG_NO_PROBE:-}" != 1 ] && port_in_use "$SCRATCH_PG_PORT" &&
            die "port $SCRATCH_PG_PORT (SCRATCH_PG_PORT) is already in use; refusing"
        echo "$SCRATCH_PG_PORT"
        return
    fi
    # Start from a per-directory offset so worktrees rarely collide, then
    # walk forward past busy ports. A race with another job picking the
    # same port makes our `pg_ctl start` fail, which is fatal below.
    local base p i
    base=$((56000 + $(printf '%s' "$DIR" | cksum | cut -d' ' -f1) % 4000))
    for i in $(seq 0 199); do
        p=$((base + i))
        if ! port_in_use "$p"; then
            echo "$p"
            return
        fi
    done
    die "no free port in $base..$((base + 199))"
}

su_url() { echo "postgres://postgres@127.0.0.1:$(cat "$PORT_FILE")/$1?sslmode=disable"; }

# owned_psql DBNAME [psql args...]: run psql against our port, but the
# session's first act is to prove the server's data directory is ours.
# Pass the SQL on stdin; it runs only after the guard.
owned_psql() {
    local dbname=$1
    shift
    {
        printf '%s\n' "SELECT current_setting('data_directory') = :'want' AS owned \\gset"
        printf '%s\n' '\if :owned' '\else' \
            "\\warn 'scratch-pg: server on this port is not ours (data_directory mismatch); refusing'" \
            '\quit 3' '\endif'
        cat
    } | psql "$(su_url "$dbname")" -q -X -v ON_ERROR_STOP=1 -v want="$PGDATA_DIR" "$@"
}

verify_owned() {
    echo 'SELECT 1;' | owned_psql postgres -o /dev/null ||
        die "server on port $(cat "$PORT_FILE") is not the cluster at $PGDATA_DIR; refusing"
}

running() { [ -f "$PORT_FILE" ] && pg_ctl -D "$PGDATA_DIR" status >/dev/null 2>&1; }

start() {
    if running; then
        log "reusing cluster at $PGDATA_DIR on port $(cat "$PORT_FILE")"
        return
    fi
    if [ ! -f "$PGDATA_DIR/PG_VERSION" ]; then
        initdb -D "$PGDATA_DIR" -U postgres --auth=trust -E UTF8 >"$DIR/initdb.log" 2>&1 ||
            die "initdb failed (see $DIR/initdb.log)"
    fi
    pick_port >"$PORT_FILE"
    local port
    port="$(cat "$PORT_FILE")"
    # TCP only: a Unix socket under a worktree path overruns the 103-byte
    # sun_path limit on macOS and the postmaster refuses to start.
    pg_ctl -D "$PGDATA_DIR" -l "$DIR/postgres.log" -w -t 30 \
        -o "-p $port -c listen_addresses=127.0.0.1 -c unix_socket_directories=''" start >/dev/null ||
        die "pg_ctl start failed on port $port (see $DIR/postgres.log); nothing was run"
    log "started cluster at $PGDATA_DIR on port $port"
}

print_env() {
    local port
    port="$(cat "$PORT_FILE")"
    echo "export DATABASE_URL='postgres://atlas_migrate@127.0.0.1:$port/$DB?sslmode=disable'"
    echo "export DATABASE_URL_APP='postgres://atlas_app@127.0.0.1:$port/$DB?sslmode=disable'"
}

case "$cmd" in
up)
    start
    verify_owned
    owned_psql postgres <<SQL
DROP DATABASE IF EXISTS $DB WITH (FORCE);
CREATE DATABASE $DB;
SQL
    owned_psql "$DB" <<SQL
\i $REPO_ROOT/migrations/bootstrap/01-roles.sql
ALTER DATABASE $DB OWNER TO atlas_migrate;
ALTER SCHEMA public OWNER TO atlas_migrate;
SQL
    port="$(cat "$PORT_FILE")"
    export REPO_ROOT
    DATABASE_URL="postgres://atlas_migrate@127.0.0.1:$port/$DB?sslmode=disable" \
        sh "$REPO_ROOT/deploy/docker/bootstrap/migrate.sh" >"$DIR/migrate.log" 2>&1 ||
        die "migrate failed (see $DIR/migrate.log)"
    log "migrated; eval the lines below (or: eval \"\$(scripts/scratch-pg.sh env)\")"
    print_env
    ;;
env)
    running || die "no running cluster at $PGDATA_DIR; run '$0 up' first"
    verify_owned
    print_env
    ;;
down)
    if [ -f "$PGDATA_DIR/PG_VERSION" ]; then
        # pg_ctl stop -D signals the postmaster recorded in OUR data
        # directory, never whatever happens to own the port.
        pg_ctl -D "$PGDATA_DIR" -m fast -w stop >/dev/null 2>&1 || true
    fi
    # Remove only what this script created, so a mistyped DIR cannot
    # take anything else with it.
    rm -rf "${PGDATA_DIR:?}"
    rm -f "$PORT_FILE" "$DIR/initdb.log" "$DIR/postgres.log" "$DIR/migrate.log"
    rmdir "$DIR" 2>/dev/null || true
    log "removed cluster at $PGDATA_DIR"
    ;;
*)
    sed -n '/^# Usage:/,/^# DIR defaults/p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 2
    ;;
esac
