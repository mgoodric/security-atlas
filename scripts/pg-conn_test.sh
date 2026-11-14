#!/usr/bin/env bash
# Self-test for deploy/docker/bootstrap/pg-conn.sh (security-atlas#2005).
#
# pg_conn_split must move the password out of DATABASE_URL into PGPASSWORD
# and leave a password-free PSQL_URL, because migrate.sh and bootstrap.sh
# hand PSQL_URL to psql's argv. The helper is sourced by `sh` in the
# bootstrap image (busybox ash) and on adopters' machines (dash, bash), so
# each case runs under every POSIX shell found on PATH.
#
# Run: bash scripts/pg-conn_test.sh
# Exits non-zero if any case fails.

set -eu

HELPER="$(cd "$(dirname "$0")/.." && pwd)/deploy/docker/bootstrap/pg-conn.sh"

shells=()
for s in sh dash bash "busybox sh"; do
    if command -v "${s%% *}" >/dev/null 2>&1; then
        shells+=("$s")
    fi
done

pass=0
fail=0

# check URL EXPECTED_PSQL_URL EXPECTED_PGPASSWORD
# EXPECTED_PGPASSWORD "<unset>" means PGPASSWORD must stay unset.
check() {
    url=$1 want_url=$2 want_pw=$3
    for s in "${shells[@]}"; do
        # SC2086: "busybox sh" splits into command + arg. SC2016: the -c
        # script expands in the child shell, not here.
        # shellcheck disable=SC2086,SC2016
        got="$(env -u PGPASSWORD DATABASE_URL="$url" HELPER="$HELPER" $s -c '
            set -eu
            . "$HELPER"
            pg_conn_split
            printf "%s\n%s" "$PSQL_URL" "${PGPASSWORD-<unset>}"
        ')"
        got_url=${got%%$'\n'*}
        got_pw=${got#*$'\n'}
        if [ "$got_url" = "$want_url" ] && [ "$got_pw" = "$want_pw" ]; then
            pass=$((pass + 1))
        else
            fail=$((fail + 1))
            echo "FAIL [$s] $url"
            echo "  PSQL_URL   got '$got_url' want '$want_url'"
            echo "  PGPASSWORD got '$got_pw' want '$want_pw'"
        fi
    done
}

check "postgres://atlas_migrate:s3cret@postgres:5432/security_atlas?sslmode=disable" \
    "postgres://atlas_migrate@postgres:5432/security_atlas?sslmode=disable" "s3cret"
check "postgresql://atlas_migrate:p%40ss%3Aw%2Fd%25@db/x" \
    "postgresql://atlas_migrate@db/x" "p@ss:w/d%"
check "postgres://u:a:b@h/d" "postgres://u@h/d" "a:b"
check "postgres://:only-pw@h/d" "postgres://h/d" "only-pw"
check "postgres://atlas_migrate@postgres:5432/security_atlas?sslmode=disable" \
    "postgres://atlas_migrate@postgres:5432/security_atlas?sslmode=disable" "<unset>"
check "postgres://h/d?options=a@b" "postgres://h/d?options=a@b" "<unset>"
check "postgres://localhost" "postgres://localhost" "<unset>"
check "host=localhost dbname=x" "host=localhost dbname=x" "<unset>"

echo "pg-conn_test: ${#shells[@]} shell(s) [${shells[*]}], $pass passed, $fail failed"
[ "$fail" -eq 0 ]
