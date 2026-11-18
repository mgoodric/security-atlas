#!/usr/bin/env bash
# Self-test for deploy/docker/bootstrap/http-endpoint.sh (security-atlas#2353).
#
# bootstrap.sh sends an OAuth client secret and a bearer to ATLAS_HTTP_URL.
# atlas_http_flag must add --allow-insecure-http only for the one trusted
# private host (ATLAS_BOOTSTRAP_PLAINTEXT_HOST, default `atlas`), leave https
# and loopback alone, and refuse every other plain http:// URL. The helper is
# sourced by `sh` in the bootstrap image (busybox ash) and on adopters'
# machines (dash, bash), so each case runs under every POSIX shell on PATH.
#
# Run: bash scripts/http-endpoint_test.sh
# Exits non-zero if any case fails.

set -eu

HELPER="$(cd "$(dirname "$0")/.." && pwd)/deploy/docker/bootstrap/http-endpoint.sh"

shells=()
for s in sh dash bash "busybox sh"; do
    if command -v "${s%% *}" >/dev/null 2>&1; then
        shells+=("$s")
    fi
done

pass=0
fail=0

# check URL WANT [TRUSTED_HOST]
# WANT is "flag" (allowed, --allow-insecure-http), "none" (allowed, no flag)
# or "refuse" (non-zero, error on stderr). TRUSTED_HOST, when given, is
# exported as ATLAS_BOOTSTRAP_PLAINTEXT_HOST.
check() {
    url=$1 want=$2 trusted=${3-}
    for s in "${shells[@]}"; do
        # SC2086: "busybox sh" splits into command + arg. SC2016: the -c
        # script expands in the child shell, not here.
        # shellcheck disable=SC2086,SC2016
        if [ -n "$trusted" ]; then
            out="$(ATLAS_BOOTSTRAP_PLAINTEXT_HOST="$trusted" URL="$url" HELPER="$HELPER" $s -c '
                set -eu
                . "$HELPER"
                if atlas_http_flag "$URL" 2>/dev/null; then printf "ok:%s" "$INSECURE_HTTP_FLAG"; else printf refuse; fi
            ')"
        else
            out="$(env -u ATLAS_BOOTSTRAP_PLAINTEXT_HOST URL="$url" HELPER="$HELPER" $s -c '
                set -eu
                . "$HELPER"
                if atlas_http_flag "$URL" 2>/dev/null; then printf "ok:%s" "$INSECURE_HTTP_FLAG"; else printf refuse; fi
            ')"
        fi
        case $want in
            flag) want_out="ok:--allow-insecure-http" ;;
            none) want_out="ok:" ;;
            *) want_out="refuse" ;;
        esac
        if [ "$out" = "$want_out" ]; then
            pass=$((pass + 1))
        else
            fail=$((fail + 1))
            echo "FAIL [$s] $url (trusted=${trusted:-<default>}): got '$out', want '$want_out'" >&2
        fi
    done
}

# The compose service gets the opt-out; nothing else does.
check "http://atlas:8080" flag
check "http://atlas" flag
check "http://ATLAS:8080/" flag
check "http://atlas:8080/path?q=1" flag

# Lookalikes of the trusted host are refused.
check "http://evil.example" refuse
check "http://evil.example:8080" refuse
check "http://atlas.evil.example" refuse
check "http://atlas.evil.example:8080" refuse
check "http://atlas@evil.example:8080" refuse
check "http://evil.example/atlas" refuse
check "http://evil.example?h=atlas" refuse
check "http://atlasx:8080" refuse
check "http://:8080" refuse

# https is unchanged: allowed, no flag.
check "https://x" none
check "https://atlas.example.com:8443" none
check "HTTPS://x" none

# Loopback needs no opt-out: atlas-cli already allows it.
check "http://localhost:8080" none
check "http://127.0.0.1:8080" none
check "http://127.10.0.3" none
check "http://[::1]:8080" none
check "http://127.0.0.1.evil.example" refuse
check "http://127.evil.example.com" refuse

# No scheme or another scheme is refused.
check "atlas:8080" refuse
check "ftp://atlas" refuse

# A deploy that names its own service (edge compose, Helm) moves the trust.
check "http://atlas-edge:8080" flag atlas-edge
check "http://atlas:8080" refuse atlas-edge
check "http://rel-security-atlas-atlas:8080" flag rel-security-atlas-atlas
check "http://atlas-edge.evil.example" refuse atlas-edge

echo "http-endpoint self-test: $pass passed, $fail failed (shells: ${shells[*]})"
[ "$fail" -eq 0 ]
