# shellcheck shell=sh disable=SC2034 # PSQL_URL is read by the sourcing script
# security-atlas — keep the atlas_migrate password off psql's argv.
#
# Sourced (not executed) by migrate.sh and bootstrap.sh. DATABASE_URL stays
# the user-facing input, but passing it to `psql "$DATABASE_URL"` puts the
# password in psql's argv, where any local user can read it from `ps` or
# /proc/<pid>/cmdline (security-atlas#2005).
#
# pg_conn_split moves the password into PGPASSWORD (exported, so psql reads
# it from the environment, which only the same user and root can see) and
# sets PSQL_URL to the same URL with the password removed. Call psql with
# "$PSQL_URL", never "$DATABASE_URL".
#
# The parse follows libpq's own URI reading: userinfo is everything before
# the first `@`, provided no `/` comes first; the password is everything
# after the first `:` in userinfo. libpq percent-decodes the password, so
# this does too before exporting it. A URL with no password (the bundled
# stack's trust / .pgpass path) leaves PGPASSWORD as the caller had it.
#
# A raw `@` inside the password splits at the first `@` (the rest lands in
# the host part), so write it `%40`; likewise write `/` as `%2F`.
#
# A `?password=...` query parameter is not moved and still reaches psql's
# argv, so pg_conn_split warns about it on stderr. Put the password in the
# userinfo, PGPASSWORD or a .pgpass file instead.

# pct_decode STRING — print STRING with %XX escapes decoded. POSIX sh only:
# each escape becomes an octal printf escape, which dash, busybox ash and
# bash all accept. Command substitution strips trailing newlines, so each
# one here (and the caller's) ends with an `x` sentinel that is removed
# afterwards; without it a decoded `%0A` would vanish. An invalid escape
# such as `%zz` stays literal. `%00` cannot survive: a shell variable
# cannot hold a NUL byte.
pct_decode() {
    _pd_s=$1
    _pd_out=
    while [ -n "$_pd_s" ]; do
        case $_pd_s in
            %[0-9A-Fa-f][0-9A-Fa-f]*)
                _pd_hex=${_pd_s#%}
                _pd_hex=${_pd_hex%"${_pd_hex#??}"}
                # shellcheck disable=SC2059 # the format IS the escape
                _pd_ch=$(printf "\\$(printf '%03o' "0x$_pd_hex")x")
                _pd_out=$_pd_out${_pd_ch%x}
                _pd_s=${_pd_s#%??}
                ;;
            *)
                _pd_out=$_pd_out${_pd_s%"${_pd_s#?}"}
                _pd_s=${_pd_s#?}
                ;;
        esac
    done
    printf '%s' "$_pd_out"
}

pg_conn_split() {
    pg_conn_split_url
    case $PSQL_URL in
        *://*\?password=* | *://*\?*\&password=*)
            echo "pg-conn: DATABASE_URL has password= in its query string," \
                "which still reaches psql's argv; put the password in the" \
                "userinfo, PGPASSWORD or a .pgpass file instead." >&2
            ;;
    esac
}

pg_conn_split_url() {
    PSQL_URL=$DATABASE_URL
    case $DATABASE_URL in
        *://*) ;;
        *) return 0 ;; # key=value conninfo: not a URL, leave it alone
    esac
    _pc_scheme=${DATABASE_URL%%://*}
    _pc_rest=${DATABASE_URL#*://}
    _pc_head=${_pc_rest%%[@/]*}
    case $_pc_rest in
        "$_pc_head@"*) ;;
        *) return 0 ;; # no userinfo
    esac
    _pc_userinfo=$_pc_head
    _pc_host=${_pc_rest#"$_pc_head@"}
    case $_pc_userinfo in
        *:*) ;;
        *) return 0 ;; # user but no password
    esac
    _pc_user=${_pc_userinfo%%:*}
    PGPASSWORD=$(
        pct_decode "${_pc_userinfo#*:}"
        printf x
    )
    PGPASSWORD=${PGPASSWORD%x}
    export PGPASSWORD
    if [ -n "$_pc_user" ]; then
        PSQL_URL="$_pc_scheme://$_pc_user@$_pc_host"
    else
        PSQL_URL="$_pc_scheme://$_pc_host"
    fi
}
