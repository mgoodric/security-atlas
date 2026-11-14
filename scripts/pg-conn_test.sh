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

errf=$(mktemp)
trap 'rm -f "$errf"' EXIT

# check URL EXPECTED_PSQL_URL EXPECTED_PGPASSWORD [warn]
# EXPECTED_PGPASSWORD "<unset>" means PGPASSWORD must stay unset. A fourth
# argument "warn" means pg_conn_split must print the ?password= warning on
# stderr; without it, stderr must stay empty.
check() {
    url=$1 want_url=$2 want_pw=$3 want_warn=${4-}
    for s in "${shells[@]}"; do
        # SC2086: "busybox sh" splits into command + arg. SC2016: the -c
        # script expands in the child shell, not here. The trailing x keeps
        # a password's trailing newline through the command substitution.
        # shellcheck disable=SC2086,SC2016
        got="$(env -u PGPASSWORD DATABASE_URL="$url" HELPER="$HELPER" $s -c '
            set -eu
            . "$HELPER"
            pg_conn_split
            printf "%s\n%sx" "$PSQL_URL" "${PGPASSWORD-<unset>}"
        ' 2>"$errf")"
        got=${got%x}
        got_url=${got%%$'\n'*}
        got_pw=${got#*$'\n'}
        got_err=$(cat "$errf")
        if [ "$want_warn" = warn ]; then
            case $got_err in
                *"password= in its query string"*) err_ok=1 ;;
                *) err_ok= ;;
            esac
        else
            [ -z "$got_err" ] && err_ok=1 || err_ok=
        fi
        if [ "$got_url" = "$want_url" ] && [ "$got_pw" = "$want_pw" ] && [ -n "$err_ok" ]; then
            pass=$((pass + 1))
        else
            fail=$((fail + 1))
            echo "FAIL [$s] $url"
            echo "  PSQL_URL   got '$got_url' want '$want_url'"
            echo "  PGPASSWORD got '$got_pw' want '$want_pw'"
            echo "  stderr     got '$got_err' want ${want_warn:-empty}"
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

# #2021: a decoded newline survives, trailing or not.
check "postgres://u:a%0Ab@h/d" "postgres://u@h/d" $'a\nb'
check "postgres://u:ab%0A%0a@h/d" "postgres://u@h/d" $'ab\n\n'
# IPv6 literal host: the brackets and port stay in PSQL_URL.
check "postgres://u:p@[::1]:5432/d" "postgres://u@[::1]:5432/d" "p"
# Multi-byte UTF-8 decodes byte by byte.
check "postgres://u:caf%C3%A9@h/d" "postgres://u@h/d" "café"
# An invalid escape stays literal.
check "postgres://u:a%zzb%4@h/d" "postgres://u@h/d" "a%zzb%4"
# `#` and `?` before the first `@` are part of the password.
check "postgres://u:p#w?x@h/d" "postgres://u@h/d" "p#w?x"
# A raw `@` splits at the first one: the rest of the password lands in the
# host part. Write it as %40 (see the case above).
check "postgres://u:p@ss@h/d" "postgres://u@ss@h/d" "p"
# A ?password= query parameter is not moved, so it warns.
check "postgres://u@h/d?password=s3cret" "postgres://u@h/d?password=s3cret" "<unset>" warn
check "postgres://h/d?sslmode=disable&password=s3cret" \
    "postgres://h/d?sslmode=disable&password=s3cret" "<unset>" warn
check "postgres://u:p@h/d?sslmode=disable" "postgres://u@h/d?sslmode=disable" "p"

echo "pg-conn_test: ${#shells[@]} shell(s) [${shells[*]}], $pass passed, $fail failed"
[ "$fail" -eq 0 ]
