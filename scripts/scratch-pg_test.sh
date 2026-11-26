#!/usr/bin/env bash
# Self-test for scripts/scratch-pg.sh (security-atlas#2033).
#
# Stands up a "foreign" cluster (standing in for another job's scratch
# DB) holding a sentinel row, then drives scratch-pg.sh into the two
# ways it used to clobber such a cluster and asserts it refuses and the
# sentinel survives:
#
#   1. port taken: SCRATCH_PG_PORT names the foreign cluster's port, so
#      our start cannot bind it (caught by the port probe, and with the
#      probe skipped, by the failed `pg_ctl start`).
#   2. stale port file: our own cluster is running, but its recorded
#      port points at the foreign cluster, so a naive script would
#      connect there and DROP DATABASE.
#
# Then a positive case: `up` on a free port migrates, and `down` stops
# only our cluster.
#
# Run: bash scripts/scratch-pg_test.sh
# Skips (exit 0) when initdb/pg_ctl/psql are not on PATH.

# SC2015: ok/bad always return 0, so `A && ok || bad` is a safe if/else.
# SC2016: the $$-quoted SQL literal is meant for psql, not the shell.
# shellcheck disable=SC2015,SC2016

set -euo pipefail

for b in initdb pg_ctl psql; do
    if ! command -v "$b" >/dev/null 2>&1; then
        echo "SKIP: $b not on PATH"
        exit 0
    fi
done

SCRIPT="$(cd "$(dirname "$0")" && pwd -P)/scratch-pg.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/scratch-pg-test.XXXXXX")"
WORK="$(cd "$WORK" && pwd -P)"
FOREIGN="$WORK/foreign"

cleanup() {
    pg_ctl -D "$FOREIGN" -m immediate -w stop >/dev/null 2>&1 || true
    pg_ctl -D "$WORK/mine/pgdata" -m immediate -w stop >/dev/null 2>&1 || true
    rm -rf "${WORK:?}"
}
trap cleanup EXIT

pass=0
fail=0
ok() {
    echo "PASS: $1"
    pass=$((pass + 1))
}
bad() {
    echo "FAIL: $1"
    fail=$((fail + 1))
}

free_port() {
    local p
    for p in $(seq 57000 57999); do
        if ! (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null; then
            echo "$p"
            return
        fi
    done
    return 1
}

# The foreign cluster: a database named `atlas` with a sentinel row.
FPORT="$(free_port)"
initdb -D "$FOREIGN" -U postgres --auth=trust >/dev/null
pg_ctl -D "$FOREIGN" -l "$WORK/foreign.log" -w \
    -o "-p $FPORT -c listen_addresses=127.0.0.1 -k $WORK" start >/dev/null
FURL="postgres://postgres@127.0.0.1:$FPORT"
psql -q -X "$FURL/postgres" -c 'CREATE DATABASE atlas'
psql -q -X "$FURL/atlas" -c 'CREATE TABLE sentinel(v text); INSERT INTO sentinel VALUES ($$alive$$)'

sentinel_alive() {
    [ "$(psql -X -At "$FURL/atlas" -c 'SELECT v FROM sentinel' 2>/dev/null)" = alive ]
}

# --- Case 1: forced port is taken by the foreign cluster ---
if SCRATCH_PG_PORT="$FPORT" bash "$SCRIPT" up "$WORK/mine" >"$WORK/case1.out" 2>&1; then
    bad "case 1: up exited 0 with the port held by a foreign cluster"
else
    ok "case 1: up refused (port held by a foreign cluster)"
fi
if sentinel_alive; then ok "case 1: foreign atlas DB intact"; else bad "case 1: foreign atlas DB was touched"; fi
grep -q 'DROP\|migrated' "$WORK/case1.out" && bad "case 1: SQL ran after the failed start" || ok "case 1: no SQL ran"

# --- Case 1b: the probe misses (a race), so pg_ctl start itself fails ---
if SCRATCH_PG_NO_PROBE=1 SCRATCH_PG_PORT="$FPORT" bash "$SCRIPT" up "$WORK/mine" >"$WORK/case1b.out" 2>&1; then
    bad "case 1b: up exited 0 after pg_ctl start failed"
else
    ok "case 1b: up refused (pg_ctl start failed)"
fi
grep -q 'pg_ctl start failed' "$WORK/case1b.out" &&
    ok "case 1b: refusal names the failed start" || bad "case 1b: refusal message missing"
if sentinel_alive; then ok "case 1b: foreign atlas DB intact"; else bad "case 1b: foreign atlas DB was touched"; fi

# --- Positive case: up on a free port ---
if bash "$SCRIPT" up "$WORK/mine" >"$WORK/up.out" 2>&1; then
    ok "up: migrated on a free port"
else
    bad "up: failed on a free port"
    cat "$WORK/up.out"
fi
grep -q "^export DATABASE_URL_APP='postgres://atlas_app@127.0.0.1:" "$WORK/up.out" &&
    ok "up: printed DATABASE_URL_APP" || bad "up: no DATABASE_URL_APP line"
MYPORT="$(cat "$WORK/mine/port")"
[ "$MYPORT" != "$FPORT" ] && ok "up: chose a port other than the foreign one" || bad "up: reused the foreign port"

# --- Case 2: our cluster runs, but the port file points at the foreign one ---
echo "$FPORT" >"$WORK/mine/port"
if bash "$SCRIPT" up "$WORK/mine" >"$WORK/case2.out" 2>&1; then
    bad "case 2: up exited 0 against a foreign data_directory"
else
    ok "case 2: up refused (data_directory mismatch)"
fi
grep -q 'not ours\|not the cluster' "$WORK/case2.out" &&
    ok "case 2: refusal names the data_directory check" || bad "case 2: refusal message missing"
if sentinel_alive; then ok "case 2: foreign atlas DB intact"; else bad "case 2: foreign atlas DB was dropped"; fi
echo "$MYPORT" >"$WORK/mine/port"

# --- down stops only our cluster ---
bash "$SCRIPT" down "$WORK/mine" >/dev/null 2>&1
[ ! -d "$WORK/mine/pgdata" ] && ok "down: removed our data directory" || bad "down: data directory left behind"
if sentinel_alive; then ok "down: foreign cluster still serving"; else bad "down: foreign cluster affected"; fi

echo "scratch-pg_test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
