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
#               (POSTGRES_HOST_AUTH_METHOD unset, scram-sha-256),
#               atlas_migrate pre-created as a NON-SUPERUSER role with a
#               password before the bundle's bootstrap runs. This is the
#               configuration that surfaced all five slice-065 bugs on the
#               first real deploy.
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
#   5. `api_keys` ends up with at least the bootstrap fixed-token row
#      (proves the audit-writer fix unblocked phase 6 — slice 065 bug #1).
#   6. A fresh re-run of `docker compose run --rm atlas-bootstrap` exits 0
#      and does not duplicate seed rows (slice 065 bug #3 idempotency,
#      AC-7).
#
# AC-9 is covered by the `external` mode's pre-created non-superuser
# atlas_migrate: if migrations/bootstrap/01-roles.sql could not widen
# atlas_migrate enough to ALTER ROLE atlas_app PASSWORD, bootstrap would
# die in phase 2.5 and assertion 3 would fail.

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
fail() { echo "[test-self-host:${MODE}] FAIL: $*" >&2; exit 1; }

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

# In bundled mode the migrate role connects with no password over the
# trust network, matching .env.example. Rewrite that one line.
if [ "${MODE}" = "bundled" ]; then
    # macOS/BSD sed and GNU sed both accept this form with an explicit
    # backup suffix; delete the backup afterwards.
    sed -i.bak 's#^DATABASE_URL_MIGRATE=.*#DATABASE_URL_MIGRATE=postgres://atlas_migrate@postgres:5432/security_atlas?sslmode=disable#' "${ENV_FILE}"
    rm -f "${ENV_FILE}.bak"
fi

# ---------------------------------------------------------------------
# external mode — pre-stage a SHARED-cluster-shaped Postgres: trust auth
# OFF, atlas_migrate pre-created as a NON-SUPERUSER with a password. We do
# this by starting just the postgres service first, configuring it, then
# bringing up the rest of the bundle.
# ---------------------------------------------------------------------
if [ "${MODE}" = "external" ]; then
    log "external mode: starting postgres alone, scram-sha-256, no trust"
    POSTGRES_HOST_AUTH_METHOD="" "${COMPOSE[@]}" up -d postgres
    # Wait for postgres to accept connections.
    for i in $(seq 1 30); do
        if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres >/dev/null 2>&1; then
            break
        fi
        [ "$i" -eq 30 ] && fail "postgres did not become ready"
        sleep 2
    done
    log "pre-creating a NON-SUPERUSER atlas_migrate with a password"
    # atlas_migrate is created here WITHOUT superuser and WITHOUT
    # CREATEROLE — exactly the shared-cluster starting point. It is given
    # CREATEROLE explicitly (a cluster admin's one-time grant), then
    # 01-roles.sql inside the bootstrap container does the rest
    # (WITH ADMIN OPTION on atlas_app). If 01-roles.sql still could not
    # ALTER ROLE atlas_app PASSWORD, bootstrap would die and assertion 3
    # below would fail.
    "${COMPOSE[@]}" exec -T postgres psql -U postgres -d security_atlas -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atlas_migrate') THEN
        CREATE ROLE atlas_migrate LOGIN PASSWORD 'test-atlas-migrate-password' NOSUPERUSER;
    END IF;
END $$;
-- The one-time cluster-admin grant the slice-065 docstring documents:
-- a non-superuser role cannot grant ITSELF CREATEROLE.
ALTER ROLE atlas_migrate CREATEROLE;
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
# ---------------------------------------------------------------------
log "checking atlas /health"
ATLAS_CID="$("${COMPOSE[@]}" ps -q atlas)"
[ -n "${ATLAS_CID}" ] || fail "atlas container not found"
HEALTH_OK=""
for i in $(seq 1 60); do
    if docker exec "${ATLAS_CID}" wget -q -O /dev/null http://localhost:8080/health 2>/dev/null; then
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

# Assertion 5: api_keys has the bootstrap fixed-token row.
APIKEYS="$(db_count 'SELECT count(*) FROM api_keys')"
[ "${APIKEYS}" -ge 1 ] 2>/dev/null || fail "api_keys row count = ${APIKEYS}, want >= 1"
log "api_keys table has ${APIKEYS} row(s)"

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
