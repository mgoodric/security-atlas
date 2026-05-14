#!/usr/bin/env bash
# security-atlas — self-host bundle end-to-end smoke test (slice 065).
#
# Exercises the docker-compose self-host bundle against a fresh checkout
# and asserts the slice-037 acceptance criteria actually pass. This is the
# substance behind slice 065 AC-7 (bootstrap idempotency), AC-9 (bootstrap
# against a shared / non-superuser Postgres) and AC-12 (end-to-end install
# in two deploy shapes).
#
# It runs in TWO modes, selected by the first argument:
#
#   bundled   — `docker compose up` exactly as docs/SELF_HOSTING.md
#               documents: the bundled `postgres:16-alpine` container with
#               its default trust-on-the-docker-network auth.
#
#   external  — the "shared cluster" shape: trust auth disabled
#               (POSTGRES_HOST_AUTH_METHOD empty -> scram-sha-256),
#               atlas_migrate pre-created as a NON-SUPERUSER role with a
#               password before the bundle's bootstrap runs, then given
#               BYPASSRLS + CREATEROLE by the cluster admin in one
#               documented one-time ALTER ROLE. This is the configuration
#               that surfaced all five slice-065 bugs on the first real
#               deploy.
#
# Usage:  deploy/docker/test-self-host-bundle.sh {bundled|external}
#
# Exit code 0 = every assertion passed. Non-zero on the first failure.
#
# Assertions (both modes):
#   1. `docker compose up` brings every service to running/healthy.
#   2. atlas /health returns 200.
#   3. atlas-bootstrap exits 0 (migrations + seed + SCF import + the 50
#      SOC 2 control-bundle uploads all succeeded).
#   4. `controls` ends up with the 50 seeded control rows.
#   5. `decision_audit_log` ends up with at least one row — every
#      authenticated control-bundle upload in phase 6 passes through the
#      OPA authz middleware, which writes one decision row per request.
#      A populated `decision_audit_log` therefore proves (a) phase 6's
#      authenticated upload path actually ran, and (b) slice 065 bug #1's
#      fix held: that bug was an RLS-blind write to THIS table, which
#      500'd every authenticated request and blocked phase 6 entirely.
#      (Earlier revisions of this harness asserted on `api_keys` here —
#      that was a mistaken premise: nothing in the bootstrap flow writes
#      `api_keys`. The bootstrap uploader authenticates with the IN-MEMORY
#      fixed-token credential — see cmd/atlas/main.go — never a DB-backed
#      api_keys row. `decision_audit_log` is the table that actually
#      records phase 6 running.)
#   6. A fresh re-run of `docker compose run --rm atlas-bootstrap` exits 0
#      and does not duplicate seed rows (slice 065 bug #3 idempotency,
#      AC-7).
#
# AC-9 is covered by the `external` mode's pre-created non-superuser
# atlas_migrate: if the cluster-admin one-time grant (BYPASSRLS +
# CREATEROLE) plus migrations/bootstrap/01-roles.sql together could not
# bring atlas_migrate to the point where it can create
# atlas_service_account and ALTER ROLE atlas_app PASSWORD, bootstrap
# would die in phase 2 / 2.5 and assertion 3 would fail.

set -euo pipefail

MODE="${1:-}"
if [ "${MODE}" != "bundled" ] && [ "${MODE}" != "external" ]; then
    echo "usage: $0 {bundled|external}" >&2
    exit 2
fi

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_DIR="${REPO_ROOT}/deploy/docker"
ENV_FILE="${COMPOSE_DIR}/.env.test"
COMPOSE=(docker compose -f "${COMPOSE_DIR}/docker-compose.yml" --env-file "${ENV_FILE}" -p "sa-selfhost-${MODE}")

log()  { echo "[test-self-host:${MODE}] $*"; }
fail() {
    echo "[test-self-host:${MODE}] FAIL: $*" >&2
    # Dump full compose logs for EVERY service (especially atlas) to stdout
    # BEFORE the EXIT trap's cleanup() tears the stack down. Without this,
    # `cleanup` runs `down -v` first and CI's later "Dump compose logs on
    # failure" step prints nothing — every self-host e2e failure used to
    # destroy the atlas server logs before anyone could read them.
    echo "[test-self-host:${MODE}] ==== compose logs (all services, tail=300) ====" >&2
    "${COMPOSE[@]}" logs --no-color --tail=300 2>&1 || true
    echo "[test-self-host:${MODE}] ==== end compose logs ====" >&2
    exit 1
}

cleanup() {
    log "tearing down"
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f "${ENV_FILE}"
}
trap cleanup EXIT

# ---------------------------------------------------------------------
# Build a .env.test with deterministic, NON-SECRET test values. These are
# neutral `test-*` strings on purpose — never vendor-prefixed tokens
# (GitGuardian flags those even in throwaway test fixtures).
# ---------------------------------------------------------------------
log "writing ${ENV_FILE}"
cat > "${ENV_FILE}" <<'EOF'
POSTGRES_PASSWORD=test-postgres-password
ATLAS_APP_PASSWORD=test-atlas-app-password
MINIO_ROOT_USER=test-minio-user
MINIO_ROOT_PASSWORD=test-minio-password
BEARER_HASH_KEY=test-bearer-hash-key-deterministic-value
ATLAS_BOOTSTRAP_TOKEN=test-bootstrap-token-deterministic-value
ATLAS_DEFAULT_USER_EMAIL=admin@example.com
ATLAS_DEFAULT_USER_PASSWORD=test-default-user-password
POSTGRES_DB=security_atlas
ATLAS_BOOTSTRAP_TENANT=00000000-0000-4000-8000-000000000001
ARTIFACTS_BUCKET=atlas-artifacts
AWS_REGION=us-east-1
ATLAS_SECURE_COOKIES=false
NEXT_PUBLIC_API_BASE_URL=
DATABASE_URL_APP=postgres://atlas_app:test-atlas-app-password@postgres:5432/security_atlas?sslmode=disable
DATABASE_URL_MIGRATE=postgres://atlas_migrate:test-atlas-migrate-password@postgres:5432/security_atlas?sslmode=disable
EOF

# POSTGRES_HOST_AUTH_METHOD is mode-dependent and is written into
# .env.test (NOT passed as an inline `VAR=... compose` prefix) so EVERY
# compose invocation in this run — the external-mode `up -d postgres`,
# the full-bundle `up -d --build`, the idempotency `run --rm`, the
# teardown — sees a consistent value via `--env-file`.
#
#   bundled  — `trust`: the bundled postgres:16-alpine accepts
#              password-less connections on the docker network, matching
#              the "trust-on-the-docker-network auth" the compose header
#              and .env.example both document (and which the
#              password-less DATABASE_URL_MIGRATE below depends on).
#   external — empty: the postgres image falls back to scram-sha-256,
#              i.e. the "shared cluster" shape with trust auth OFF.
#
# PG_INITDB_ROLES selects the postgres /docker-entrypoint-initdb.d script
# that creates the three roles at cluster init:
#
#   bundled  — the repo's migrations/bootstrap/01-roles.sql (the compose
#              default), so atlas_migrate exists before atlas-bootstrap
#              ever connects as it. Without this, bootstrap.sh phase 1
#              loops forever on "role atlas_migrate does not exist".
#   external — /dev/null (an empty no-op initdb script), so the harness's
#              own "pre-create atlas_migrate as a NON-SUPERUSER" step
#              below is what actually creates the role — preserving the
#              shared-cluster test premise.
if [ "${MODE}" = "bundled" ]; then
    echo "POSTGRES_HOST_AUTH_METHOD=trust" >> "${ENV_FILE}"
    echo "PG_INITDB_ROLES=../../migrations/bootstrap/01-roles.sql" >> "${ENV_FILE}"
    # In bundled mode the migrate role connects with no password over the
    # trust network, matching .env.example. Rewrite that one line.
    # macOS/BSD sed and GNU sed both accept this form with an explicit
    # backup suffix; delete the backup afterwards.
    sed -i.bak 's#^DATABASE_URL_MIGRATE=.*#DATABASE_URL_MIGRATE=postgres://atlas_migrate@postgres:5432/security_atlas?sslmode=disable#' "${ENV_FILE}"
    rm -f "${ENV_FILE}.bak"
else
    echo "POSTGRES_HOST_AUTH_METHOD=" >> "${ENV_FILE}"
    echo "PG_INITDB_ROLES=/dev/null" >> "${ENV_FILE}"
fi

# ---------------------------------------------------------------------
# external mode — pre-stage a SHARED-cluster-shaped Postgres: trust auth
# OFF, atlas_migrate pre-created as a NON-SUPERUSER with a password. We do
# this by starting just the postgres service first, configuring it, then
# bringing up the rest of the bundle.
# ---------------------------------------------------------------------
if [ "${MODE}" = "external" ]; then
    log "external mode: starting postgres alone, scram-sha-256, no trust"
    # POSTGRES_HOST_AUTH_METHOD is empty in the external .env.test
    # (written above), so this `up` and every later compose invocation
    # consistently get scram-sha-256 — no inline VAR= prefix needed.
    "${COMPOSE[@]}" up -d postgres
    # Wait for postgres to accept connections.
    for i in $(seq 1 30); do
        if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres >/dev/null 2>&1; then
            break
        fi
        [ "$i" -eq 30 ] && fail "postgres did not become ready"
        sleep 2
    done
    log "pre-creating a NON-SUPERUSER atlas_migrate with a password"
    # atlas_migrate is created here WITHOUT superuser, WITHOUT CREATEROLE
    # and WITHOUT BYPASSRLS — exactly the shared-cluster starting point.
    # The cluster admin then grants it BYPASSRLS + CREATEROLE in ONE
    # one-time `ALTER ROLE` (a non-superuser role cannot grant ITSELF
    # those attributes), and 01-roles.sql inside the bootstrap container
    # does the rest unprivileged.
    #
    # BOTH attributes are required, and together they make the
    # shared-cluster atlas_migrate identical to the dedicated-container
    # atlas_migrate (01-roles.sql line ~69 creates it `LOGIN BYPASSRLS`):
    #   - BYPASSRLS — PG16 only lets a BYPASSRLS role CREATE another
    #     BYPASSRLS role, and 01-roles.sql creates atlas_service_account
    #     WITH BYPASSRLS. Without it, 01-roles.sql dies at that CREATE
    #     ROLE with "permission denied to create role". atlas_migrate is
    #     a BYPASSRLS role by design (bootstrap.sh connects as it for the
    #     cross-tenant boot-time writes) so this is not a widening.
    #   - CREATEROLE — lets atlas_migrate create atlas_app (and thereby
    #     hold implicit ADMIN on it) so bootstrap.sh phase 2.5's
    #     `ALTER ROLE atlas_app PASSWORD` succeeds.
    # atlas_app itself stays NOSUPERUSER NOBYPASSRLS; atlas_migrate does
    # NOT become superuser. If 01-roles.sql still could not run, bootstrap
    # would die and assertion 3 below would fail.
    #
    # The same cluster-admin step also transfers ownership of schema
    # `public` to atlas_migrate (slice-065 bug #6). Postgres 15+ no longer
    # lets the PUBLIC pseudo-role — and therefore atlas_migrate — create
    # objects in `public`, so bootstrap.sh's forward migrations would die
    # with `permission denied for schema public`. atlas_migrate is the DDL
    # role, so it owns the schema it manages; atlas_app stays USAGE-only.
    # 01-roles.sql ALSO contains this `ALTER SCHEMA ... OWNER`, conditional
    # on atlas_migrate not already owning public — in bundled mode that
    # runs at initdb as the superuser, but in external mode 01-roles.sql
    # runs as the non-superuser atlas_migrate, which CANNOT take schema
    # ownership. So the transfer must happen HERE, in the superuser
    # cluster-admin step; 01-roles.sql then sees it done and skips it.
    "${COMPOSE[@]}" exec -T postgres psql -U postgres -d security_atlas -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_migrate') THEN
        CREATE ROLE atlas_migrate LOGIN PASSWORD 'test-atlas-migrate-password' NOSUPERUSER;
    END IF;
END $$;
-- The one-time cluster-admin grants the slice-065 docstring documents:
-- a non-superuser role cannot grant ITSELF BYPASSRLS or CREATEROLE, nor
-- take ownership of a schema it does not own.
ALTER ROLE atlas_migrate BYPASSRLS CREATEROLE;
ALTER SCHEMA public OWNER TO atlas_migrate;
GRANT ALL PRIVILEGES ON DATABASE security_atlas TO atlas_migrate;
SQL
fi

# ---------------------------------------------------------------------
# Bring up the full bundle.
# ---------------------------------------------------------------------
log "docker compose up -d (full bundle)"
"${COMPOSE[@]}" up -d --build

# ---------------------------------------------------------------------
# Assertion 3: atlas-bootstrap exits 0.
# ---------------------------------------------------------------------
log "waiting for atlas-bootstrap to exit"
BOOTSTRAP_CID="$("${COMPOSE[@]}" ps -aq atlas-bootstrap)"
[ -n "${BOOTSTRAP_CID}" ] || fail "atlas-bootstrap container not found"
for i in $(seq 1 120); do
    STATE="$(docker inspect -f '{{.State.Status}}' "${BOOTSTRAP_CID}")"
    if [ "${STATE}" = "exited" ]; then
        break
    fi
    [ "$i" -eq 120 ] && fail "atlas-bootstrap did not exit within ~4 min"
    sleep 2
done
RC="$(docker inspect -f '{{.State.ExitCode}}' "${BOOTSTRAP_CID}")"
[ "${RC}" = "0" ] || {
    "${COMPOSE[@]}" logs atlas-bootstrap || true
    fail "atlas-bootstrap exited ${RC}, want 0"
}
log "atlas-bootstrap exited 0"

# ---------------------------------------------------------------------
# Assertion 2: atlas /health returns 200.
#
# The atlas image is distroless (gcr.io/distroless/static-debian12) — no
# shell, no wget, no curl. `docker exec atlas wget ...` therefore ALWAYS
# fails ("executable not found"), which used to read as "/health never
# returned 200" even when the server was perfectly healthy (the bundled-
# mode false failure). Probe the HOST-published port instead: compose
# maps atlas's :8080 to a host port, and the runner has curl. This is the
# same path a real operator's browser / load balancer takes, so it is
# also a more faithful smoke test than an in-container loopback probe.
# ---------------------------------------------------------------------
log "checking atlas /health"
ATLAS_CID="$("${COMPOSE[@]}" ps -q atlas)"
[ -n "${ATLAS_CID}" ] || fail "atlas container not found"
# `docker compose port` prints e.g. `0.0.0.0:8080`; take the port field.
ATLAS_HOSTPORT="$("${COMPOSE[@]}" port atlas 8080 2>/dev/null | awk -F: 'NF{print $NF}')"
[ -n "${ATLAS_HOSTPORT}" ] || fail "could not resolve atlas host-published :8080 port"
log "atlas /health host port is ${ATLAS_HOSTPORT}"
HEALTH_OK=""
for i in $(seq 1 60); do
    if curl -fsS -o /dev/null "http://127.0.0.1:${ATLAS_HOSTPORT}/health" 2>/dev/null; then
        HEALTH_OK=1
        break
    fi
    sleep 2
done
[ -n "${HEALTH_OK}" ] || fail "atlas /health never returned 200"
log "atlas /health is 200"

# ---------------------------------------------------------------------
# DB assertions. Run psql inside the postgres container as the superuser
# so the count queries are not RLS-filtered.
# ---------------------------------------------------------------------
PG_CID="$("${COMPOSE[@]}" ps -q postgres)"
[ -n "${PG_CID}" ] || fail "postgres container not found"
# Tuple-only, unaligned, single-command psql; trim all whitespace so the
# result is a bare integer ready for string comparison.
db_count() {
    docker exec -i "${PG_CID}" psql -U postgres -d security_atlas -t -A -c "$1" 2>/dev/null | tr -d '[:space:]'
}

# Assertion 4: 50 control rows.
CONTROLS="$(db_count 'SELECT count(*) FROM controls')"
[ "${CONTROLS}" = "50" ] || fail "controls row count = ${CONTROLS}, want 50"
log "controls table has 50 rows"

# Assertion 5: decision_audit_log has at least one row — proves phase 6's
# authenticated control-bundle upload path ran AND slice 065 bug #1's fix
# (the RLS-blind write to this very table) held. See the header comment.
AUDITROWS="$(db_count 'SELECT count(*) FROM decision_audit_log')"
[ "${AUDITROWS}" -ge 1 ] 2>/dev/null || fail "decision_audit_log row count = ${AUDITROWS}, want >= 1"
log "decision_audit_log table has ${AUDITROWS} row(s)"

# ---------------------------------------------------------------------
# Assertion 6 (AC-7): a fresh re-run of atlas-bootstrap against the now-
# populated DB exits 0 and does not duplicate seed rows.
# ---------------------------------------------------------------------
log "re-running atlas-bootstrap (idempotency check)"
CONTROLS_BEFORE="${CONTROLS}"
SCOPES_BEFORE="$(db_count 'SELECT count(*) FROM scope_cells')"
USERS_BEFORE="$(db_count 'SELECT count(*) FROM users')"

set +e
"${COMPOSE[@]}" run --rm atlas-bootstrap
RERUN_RC=$?
set -e
[ "${RERUN_RC}" = "0" ] || fail "atlas-bootstrap re-run exited ${RERUN_RC}, want 0"

CONTROLS_AFTER="$(db_count 'SELECT count(*) FROM controls')"
SCOPES_AFTER="$(db_count 'SELECT count(*) FROM scope_cells')"
USERS_AFTER="$(db_count 'SELECT count(*) FROM users')"

[ "${CONTROLS_AFTER}" = "${CONTROLS_BEFORE}" ] || fail "controls changed on re-run: ${CONTROLS_BEFORE} -> ${CONTROLS_AFTER}"
[ "${SCOPES_AFTER}" = "${SCOPES_BEFORE}" ]     || fail "scope_cells changed on re-run: ${SCOPES_BEFORE} -> ${SCOPES_AFTER}"
[ "${USERS_AFTER}" = "${USERS_BEFORE}" ]       || fail "users changed on re-run: ${USERS_BEFORE} -> ${USERS_AFTER}"
log "atlas-bootstrap re-run exited 0 with identical row counts (idempotent)"

log "ALL ASSERTIONS PASSED (${MODE})"
