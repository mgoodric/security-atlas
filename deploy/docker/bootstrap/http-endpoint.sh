# shellcheck shell=sh disable=SC2034 # INSECURE_HTTP_FLAG is read by the sourcing script
# security-atlas — decide whether bootstrap may send its client secret and
# bearer to ATLAS_HTTP_URL over plain http:// (security-atlas#2353).
#
# Sourced (not executed) by bootstrap.sh. atlas-cli refuses plain http:// to
# a non-loopback host unless --allow-insecure-http is set (#2257). The
# bundled stack reaches the server as http://atlas:8080 on the compose
# network, which never leaves the host, so bootstrap opts out for that one
# host only. An operator who points ATLAS_HTTP_URL at any other http:// host
# would otherwise send the OAuth client secret and the bearer to it in the
# clear, so that fails here, before anything is sent.
#
# The one trusted host is ATLAS_BOOTSTRAP_PLAINTEXT_HOST, default `atlas`
# (docker-compose.yml). The deploy that names the private service sets it:
# docker-compose.edge.yml sets atlas-edge, the Helm migrate Job sets
# <release>-atlas.
#
# atlas_http_flag URL sets INSECURE_HTTP_FLAG and returns 0, or prints an
# error on stderr and returns 1:
#
#   https://...                       allowed, INSECURE_HTTP_FLAG empty
#   http://<trusted host>[:port]/...  allowed, INSECURE_HTTP_FLAG=--allow-insecure-http
#   http://localhost, 127.0.0.0/8,    allowed, INSECURE_HTTP_FLAG empty
#   http://[::1]                        (atlas-cli already permits loopback)
#   anything else                     refused
#
# The host is matched exactly (case-insensitive), so atlas.evil.example and
# atlas@evil.example (userinfo `atlas`, host evil.example) are refused.

atlas_http_flag() {
    INSECURE_HTTP_FLAG=
    _ah_url=$1
    _ah_scheme=$(printf '%s' "${_ah_url%%://*}" | tr '[:upper:]' '[:lower:]')
    if [ "$_ah_scheme" = "$_ah_url" ]; then
        echo "[bootstrap] ERROR: ATLAS_HTTP_URL '$_ah_url' has no scheme; use https:// or http://atlas:<port>" >&2
        return 1
    fi
    case $_ah_scheme in
        https) return 0 ;;
        http) ;;
        *)
            echo "[bootstrap] ERROR: ATLAS_HTTP_URL '$_ah_url' must be https:// or http://" >&2
            return 1
            ;;
    esac

    # authority = text after :// up to the first / ? or #; host = authority
    # without userinfo (up to the last @) and without the port.
    _ah_auth=${_ah_url#*://}
    _ah_auth=${_ah_auth%%[/?#]*}
    _ah_host=${_ah_auth##*@}
    case $_ah_host in
        \[*\]*) _ah_host=${_ah_host%%]*}] ;;
        *) _ah_host=${_ah_host%%:*} ;;
    esac
    _ah_host=$(printf '%s' "$_ah_host" | tr '[:upper:]' '[:lower:]')
    _ah_trusted=$(printf '%s' "${ATLAS_BOOTSTRAP_PLAINTEXT_HOST:-atlas}" | tr '[:upper:]' '[:lower:]')

    if [ -n "$_ah_host" ] && [ "$_ah_host" = "$_ah_trusted" ]; then
        INSECURE_HTTP_FLAG=--allow-insecure-http
        return 0
    fi
    case $_ah_host in
        localhost | \[::1\]) return 0 ;;
    esac
    # 127.0.0.0/8: four dot-separated runs of digits, the first 127.
    case $_ah_host in
        127.*.*.*)
            case $_ah_host in
                *[!0-9.]* | *..* | *. | 127.*.*.*.*) ;;
                *) return 0 ;;
            esac
            ;;
    esac
    echo "[bootstrap] ERROR: refusing to send the bootstrap client secret and bearer over plain http:// to '$_ah_host' (ATLAS_HTTP_URL=$_ah_url); use https://, the private service http://$_ah_trusted:<port> (ATLAS_BOOTSTRAP_PLAINTEXT_HOST), or a loopback address" >&2
    return 1
}
