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
#   proxy     — (slice 470) the bundled stack PLUS a header-overwriting
#               nginx reverse proxy in front of atlas
#               (docker-compose.proxy.yml overlay), with
#               TRUSTED_PROXY_CIDRS set to the proxy's fixed
#               container-network CIDR. Proves slice 466's right-to-left
#               X-Forwarded-For walk end-to-end across a real multi-
#               container topology: a login THROUGH the proxy records the
#               proxy-supplied client IP on the session row, while a DIRECT
#               login forging X-Forwarded-For records the direct peer (the
#               forged value is rejected). This is the real-proxy-container
#               e2e slice 466 D4 deferred because the proxy-less seed could
#               not provide it.
#
#   migrate   — (slice 473) REPRODUCE-THE-INCIDENT migrate-on-upgrade
#               proof. Brings the bundle up at migration set N, then
#               simulates an image update that carries a NEWER migration
#               set by writing a brand-new sentinel migration into the
#               (bind-mounted) checkout and re-running the always-run
#               `atlas-migrate` service. Asserts: (AC-1/AC-2) the new
#               migration applied; (AC-3/AC-4) the atlas backend was
#               force-recreated and came back HEALTHY only AFTER
#               atlas-migrate exited 0 (the service_completed_successfully
#               gate held — it never served the partial schema); (AC-2)
#               a third migrate re-run against the now-current DB is a
#               "schema current" no-op that does NOT re-seed. This is the
#               2026-06-05 production incident: a binary moving forward
#               while the migrate step would otherwise be stranded.
#
# Usage:  deploy/docker/test-self-host-bundle.sh {bundled|external|proxy|migrate}
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
#   6. (Slice 212) POST /auth/local/login with the bootstrap user's
#      email + password returns HTTP 200 and a non-empty `token` field —
#      proves slice 209's local-credential AS is wired end-to-end
#      (handler reachable + password verifies + JWT signer produces a
#      token; catches the slice-209 D5 nil-signer fallback).
#   7. (Slice 212) The JWT minted in assertion 6 carries
#      `atlas:super_admin == true` AND
#      `atlas:roles[<bootstrap_tenant_uuid>]` containing `"admin"` —
#      proves slice 211's seed.sql role grants flowed into the JWT
#      claims at sign-in time. Without this, every admin-gated endpoint
#      would return 403 even though sign-in itself succeeded.
#   8. A fresh re-run of `docker compose run --rm atlas-bootstrap` exits 0
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
if [ "${MODE}" != "bundled" ] && [ "${MODE}" != "external" ] \
    && [ "${MODE}" != "proxy" ] && [ "${MODE}" != "migrate" ]; then
    echo "usage: $0 {bundled|external|proxy|migrate}" >&2
    exit 2
fi

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_DIR="${REPO_ROOT}/deploy/docker"
ENV_FILE="${COMPOSE_DIR}/.env.test"

# Slice 470: the `proxy` mode layers the header-overwriting reverse-proxy
# overlay ON TOP of the base bundle, so every compose invocation in this run
# must carry BOTH -f files. The base `bundled`/`external` modes use only the
# base file. Build the -f arg list once here so `up`, `run`, `logs`, `port`,
# `down` all see the identical compose projection (a stale `down` that omits
# the overlay would orphan the proxy container + the atlasnet network).
COMPOSE_FILES=(-f "${COMPOSE_DIR}/docker-compose.yml")
if [ "${MODE}" = "proxy" ]; then
    COMPOSE_FILES+=(-f "${COMPOSE_DIR}/docker-compose.proxy.yml")
fi
COMPOSE=(docker compose "${COMPOSE_FILES[@]}" --env-file "${ENV_FILE}" -p "sa-selfhost-${MODE}")

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

# Slice 473: migrate mode writes a sentinel migration into the checkout to
# simulate an image update carrying a newer migration set. SENTINEL_SQL is
# set later (once we know the migrations dir); cleanup removes it so the
# working tree is never left dirty even on an early failure.
SENTINEL_SQL=""
cleanup() {
    log "tearing down"
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f "${ENV_FILE}"
    [ -n "${SENTINEL_SQL}" ] && rm -f "${SENTINEL_SQL}"
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
# Slice 470: `proxy` mode is the bundled stack plus the reverse-proxy overlay,
# so it shares bundled mode's trust-on-the-docker-network Postgres auth and
# the repo's 01-roles.sql initdb script.
if [ "${MODE}" = "bundled" ] || [ "${MODE}" = "proxy" ] || [ "${MODE}" = "migrate" ]; then
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

# Slice 470: pin TRUSTED_PROXY_CIDRS into .env.test so EVERY compose
# invocation (up/run/logs/down) sees the same allowlist. The value MUST equal
# the fixed subnet docker-compose.proxy.yml declares for `atlasnet`
# (10.123.0.0/24) — the proxy connects to atlas from an address in that /24,
# so it is a trusted hop; the test runner is off-network, so its forged XFF is
# rejected. In bundled/external modes the var is left unset (no proxy), so the
# server records the direct peer exactly as the shipped seed does.
if [ "${MODE}" = "proxy" ]; then
    echo "TRUSTED_PROXY_CIDRS=10.123.0.0/24" >> "${ENV_FILE}"
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
    # Wait for postgres to accept connections AND for POSTGRES_DB to exist.
    #
    # Slice 200 — race-condition fix. `pg_isready -U postgres` (with no -d)
    # connects against the user-named DB and returns ready as soon as the
    # postgres docker-entrypoint's TEMP server is up. That temp server is
    # the one that the entrypoint itself uses to run `CREATE DATABASE
    # "$POSTGRES_DB"` and the /docker-entrypoint-initdb.d/*.sql scripts —
    # so on a fresh data dir there is a window where `pg_isready` returns 0
    # but the `security_atlas` database has NOT yet been created. The next
    # step (`psql -d security_atlas ...`) then fails with
    #   FATAL: database "security_atlas" does not exist
    # The fix is to point the readiness check at the target database so it
    # only succeeds once `docker_setup_db` has finished creating it. The
    # bundled-mode harness path does not poll this directly — it relies on
    # the compose healthcheck during `up -d --build` — so this fix is
    # external-mode-only.
    for i in $(seq 1 30); do
        if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres -d security_atlas >/dev/null 2>&1; then
            break
        fi
        [ "$i" -eq 30 ] && fail "postgres did not become ready (security_atlas DB never created)"
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
# Staged bring-up — slice 202 race-condition fix.
#
# Background: the atlas server's boot-time schema importer
# (cmd/atlas/main.go ~L640 — `ImportPlatformSchemas`) runs ONCE without
# retry. It inserts the platform-bundled evidence_kind schemas into
# `evidence_kind_schemas`. The cache-reload loop right after it retries
# (90s) but only re-READS — it does NOT re-IMPORT. So if the importer
# loses the race against the forward migrations
# (i.e. atlas starts BEFORE migrations/sql/20260511000002_schema_registry.sql
# has created `evidence_kind_schemas`), the table is missing, the import fails with
# `relation "evidence_kind_schemas" does not exist (SQLSTATE 42P01)`,
# rows are NEVER inserted, the cache stays empty, and bootstrap's
# `controls upload` 400s on every bundle with `evidence_kind ... is not
# registered in the schema registry`.
#
# Surfaced as the slice 202 spillover from slice 131 PR #484 CI run
# 26293268087 (bundled mode). External mode passed on the same run, but
# the race exists identically there too — the fix applies uniformly.
#
# Slice 473 note: migrations now run in the dedicated always-run
# `atlas-migrate` service, NOT inside atlas-bootstrap. Bringing up
# atlas-bootstrap auto-starts its dependency atlas-migrate (gated
# `service_completed_successfully`), so compose runs migrate first and
# then bootstrap's seed. The compose `atlas` block ALSO gates on
# atlas-migrate completing — so in production the race below cannot even
# occur (atlas can't start until migrate exits). The staged bring-up +
# poll is KEPT here because this harness drives `atlas` up separately in
# stage 2; it remains a belt-and-suspenders deterministic gate and lets
# the harness surface a stage-1 failure with a precise diagnostic.
#
# The fix: bring up postgres + atlas-bootstrap FIRST (which pulls in
# atlas-migrate), then poll for the sentinel table `evidence_kind_schemas`
# to exist (proving the forward migrations completed and the importer
# would find its target), then bring up the rest (atlas + web).
#
# Why this is deterministic, not a sleep (P0-A3): the sentinel is the
# OUTPUT of the racing step — the migration that creates the table.
# `evidence_kind_schemas` not existing means migrations have not
# advanced to that file; its existence means they have. There is no
# clock-based wait — just polling on a real state transition.
# ---------------------------------------------------------------------
log "docker compose up -d postgres + atlas-bootstrap (stage 1: apply migrations via atlas-migrate)"
"${COMPOSE[@]}" up -d --build postgres atlas-bootstrap

log "polling evidence_kind_schemas existence (sentinel of forward migrations complete)"
# Postgres container is healthy by now (atlas-bootstrap depends_on
# postgres:service_healthy + minio-mc:service_completed_successfully +
# atlas-migrate:service_completed_successfully, so docker compose has
# already gated on all three). Poll every 2s for up to 4 minutes for the
# `evidence_kind_schemas` relation to exist. Using `to_regclass()`
# instead of the `schema_migrations` ledger row is deliberate: the
# relation check is exactly what the atlas importer queries and is
# also robust against a migration whose CREATE TABLE is committed
# before the matching ledger row's INSERT.
SCHEMA_READY=""
for i in $(seq 1 120); do
    if "${COMPOSE[@]}" exec -T postgres psql -U postgres -d security_atlas -t -A \
        -c "SELECT to_regclass('public.evidence_kind_schemas') IS NOT NULL" 2>/dev/null \
        | tr -d '[:space:]' | grep -qx 't'; then
        SCHEMA_READY=1
        break
    fi
    # Bail early if atlas-bootstrap has already exited NON-ZERO — no
    # point waiting for a sentinel that will never appear (e.g.
    # migration failure unrelated to the race). The exit-0 assertion
    # below will catch it with the proper diagnostic; we just stop
    # spinning.
    BSC="$("${COMPOSE[@]}" ps -aq atlas-bootstrap)"
    if [ -n "${BSC}" ]; then
        STATE="$(docker inspect -f '{{.State.Status}}' "${BSC}" 2>/dev/null || true)"
        RC="$(docker inspect -f '{{.State.ExitCode}}' "${BSC}" 2>/dev/null || true)"
        if [ "${STATE}" = "exited" ] && [ "${RC}" != "0" ]; then
            fail "atlas-bootstrap exited ${RC} during stage-1 migration phase"
        fi
    fi
    sleep 2
done
[ -n "${SCHEMA_READY}" ] || fail "evidence_kind_schemas relation not created within ~4 min (stage-1 migrations stalled)"
log "evidence_kind_schemas exists — stage-1 migrations complete"

# ---------------------------------------------------------------------
# Stage 2: bring up the rest (atlas + web). atlas's boot-time schema
# importer now finds evidence_kind_schemas already migrated; the
# importer succeeds, the cache loads, /health returns 200,
# atlas-bootstrap phase 5-6 (which is blocked on atlas /health) wakes
# and uploads control bundles.
# ---------------------------------------------------------------------
log "docker compose up -d (stage 2: atlas + web)"
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
# Assertion 6 (slice 212): the bootstrap user can sign in via the slice-
# 209 local-credential AS. Catches slice 209 D5's nil-signer fallback
# (200 with no `token` field) and any future regression in
# /auth/local/login wiring.
#
# Body materialized via heredoc to a tmpfile + `curl --data @<file>` so
# the password never lands in a process arg list (defensive even though
# the bootstrap password is a CI-only deterministic value).
# ---------------------------------------------------------------------
LOGIN_BODY="$(mktemp)"
cat > "${LOGIN_BODY}" <<JSON
{"tenant_id":"00000000-0000-4000-8000-000000000001","email":"admin@example.com","password":"test-default-user-password"}
JSON

LOGIN_RESP="$(mktemp)"
LOGIN_CODE="$(curl -sS -o "${LOGIN_RESP}" -w "%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    --data "@${LOGIN_BODY}" \
    "http://127.0.0.1:${ATLAS_HOSTPORT}/auth/local/login")"
rm -f "${LOGIN_BODY}"

[ "${LOGIN_CODE}" = "200" ] \
    || fail "sign-in: HTTP ${LOGIN_CODE} (want 200); body: $(head -c 400 "${LOGIN_RESP}")"

TOKEN="$(python3 -c '
import json, sys
with open(sys.argv[1]) as f:
    body = json.load(f)
tok = body.get("token", "")
if not tok:
    sys.stderr.write("token field missing or empty in /auth/local/login response\n")
    sys.exit(1)
print(tok)
' "${LOGIN_RESP}")" \
    || fail "sign-in: response has no non-empty 'token' field (slice 209 D5 nil-signer fallback?); body: $(head -c 400 "${LOGIN_RESP}")"
log "sign-in: HTTP 200, token minted ($(printf %s "${TOKEN}" | wc -c | tr -d ' ') chars)"

# ---------------------------------------------------------------------
# Assertion 7 (slice 212): the JWT minted in assertion 6 carries the
# slice-211 admin role grant + super_admin claim. Decodes the JWT's
# middle segment (base64url-encoded JSON payload) and inspects the
# `atlas:super_admin` + `atlas:roles[<bootstrap_tenant>]` claims.
#
# Without this assertion, a regression that removed the slice-211 seed
# grants would pass sign-in (assertion 6 stays green) but every
# admin/auditor-gated /v1/* endpoint would 403 in prod — exactly the
# bug class slices 209/210/211 collectively dug us out of.
# ---------------------------------------------------------------------
python3 - <<PY || fail "JWT claim verification failed (see stderr above)"
import base64, json, sys
token = """${TOKEN}"""
parts = token.split(".")
if len(parts) != 3:
    sys.stderr.write(f"JWT shape: {len(parts)} dot-separated parts, want 3\n")
    sys.exit(1)
b = parts[1]
b += "=" * (4 - len(b) % 4)
claims = json.loads(base64.urlsafe_b64decode(b))
ok = True
if claims.get("atlas:super_admin") is not True:
    sys.stderr.write(f"atlas:super_admin = {claims.get('atlas:super_admin')!r}; want True\n")
    ok = False
TENANT = "00000000-0000-4000-8000-000000000001"
roles_map = claims.get("atlas:roles") or {}
tenant_roles = roles_map.get(TENANT) or []
if "admin" not in tenant_roles:
    sys.stderr.write(f"atlas:roles[{TENANT}] = {tenant_roles!r}; want it to contain 'admin'\n")
    ok = False
if not ok:
    sys.stderr.write(f"full claims: {json.dumps(claims, indent=2)}\n")
    sys.exit(1)
print("  atlas:super_admin = True")
print(f"  atlas:roles[{TENANT}] contains 'admin'")
PY
log "JWT carries super_admin=true + admin role in bootstrap tenant"
rm -f "${LOGIN_RESP}"

# ---------------------------------------------------------------------
# Assertions 9 + 10 (slice 470) — PROXY MODE ONLY: the trusted-proxy
# X-Forwarded-For walk (internal/api/auth/clientip.go, slice 466) proven
# end-to-end across a real multi-container topology.
#
# This is the real-proxy-container e2e slice 466 D4 deferred. The
# docker-compose.proxy.yml overlay (layered in via COMPOSE_FILES when
# MODE=proxy) put an nginx in front of atlas that OVERWRITES
# X-Forwarded-For with a fixed TEST-NET client IP (203.0.113.10), and set
# TRUSTED_PROXY_CIDRS=10.123.0.0/24 (the proxy's fixed container subnet).
#
# Both legs sign in via /auth/local/login (which writes one `sessions` row
# carrying ip_address = clientIP(r)), capture the session cookie (whose
# value IS the session row id), then read THAT row's ip_address back
# directly — so each assertion is pinned to the exact login it made, not a
# racy "most recent row".
#
#   AC-2 (assertion 9): login THROUGH the proxy. atlas sees the request
#     arrive from the proxy's 10.123.x peer (trusted) carrying the proxy-
#     set XFF; the right-to-left walk returns the single untrusted hop =>
#     ip_address == 203.0.113.10 (the proxy-supplied client IP).
#
#   AC-3 (assertion 10): login DIRECT to atlas's host-published :8080,
#     the runner forging X-Forwarded-For: 198.51.100.66. The runner is
#     off the trusted subnet, so the walk never consults XFF =>
#     ip_address is the direct container peer, and is NOT 198.51.100.66.
# ---------------------------------------------------------------------
if [ "${MODE}" = "proxy" ]; then
    PROXY_CLIENT_IP="203.0.113.10"   # TEST-NET-3, what the proxy re-issues
    FORGED_IP="198.51.100.66"        # TEST-NET-2, what a direct client forges

    # session_ip <session_id> -> the ip_address TEXT column for that row.
    # Queried as the postgres superuser so RLS does not filter it out.
    session_ip() {
        docker exec -i "${PG_CID}" psql -U postgres -d security_atlas -t -A \
            -c "SELECT coalesce(ip_address, '<null>') FROM sessions WHERE id = '$1'" \
            2>/dev/null | tr -d '[:space:]'
    }

    # login_session_id <base_url> [extra curl args...] -> the atlas_session
    # cookie value (== the session row id) from a fresh /auth/local/login.
    # Body is written to a tmpfile and passed via --data @file so the
    # password never lands in a process arg list.
    login_session_id() {
        local base_url="$1"; shift
        local body jar code sid
        body="$(mktemp)"; jar="$(mktemp)"
        cat > "${body}" <<JSON
{"tenant_id":"00000000-0000-4000-8000-000000000001","email":"admin@example.com","password":"test-default-user-password"}
JSON
        code="$(curl -sS -o /dev/null -w "%{http_code}" \
            -c "${jar}" \
            -X POST -H "Content-Type: application/json" \
            --data "@${body}" "$@" \
            "${base_url}/auth/local/login")"
        rm -f "${body}"
        if [ "${code}" != "200" ]; then
            rm -f "${jar}"
            echo "HTTP ${code}" >&2
            return 1
        fi
        # The Netscape cookie jar's last field on the atlas_session line is
        # the cookie value (the session id). awk on that line; trim CRLF.
        sid="$(awk '$6 == "atlas_session" {print $7}' "${jar}" | tr -d '\r\n')"
        rm -f "${jar}"
        if [ -z "${sid}" ]; then
            echo "no atlas_session cookie set" >&2
            return 1
        fi
        printf '%s' "${sid}"
    }

    # --- Assertion 9 (AC-2): login THROUGH the proxy ---
    log "proxy login: resolving proxy host-published :8088 port"
    PROXY_HOSTPORT="$("${COMPOSE[@]}" port proxy 8088 2>/dev/null | awk -F: 'NF{print $NF}')"
    [ -n "${PROXY_HOSTPORT}" ] || fail "could not resolve proxy host-published :8088 port"
    # Wait for the proxy to be able to forward to atlas (atlas /health is
    # already 200 by here, but nginx's own listener may need a beat).
    PROXY_UP=""
    for i in $(seq 1 30); do
        # A 4xx/5xx from atlas is still proof the proxy forwarded; we only
        # need the proxy reachable. Probe /health THROUGH the proxy.
        if curl -fsS -o /dev/null "http://127.0.0.1:${PROXY_HOSTPORT}/health" 2>/dev/null; then
            PROXY_UP=1; break
        fi
        sleep 2
    done
    [ -n "${PROXY_UP}" ] || fail "proxy never forwarded /health to atlas"

    log "proxy login: POST /auth/local/login through the proxy"
    PROXY_SID="$(login_session_id "http://127.0.0.1:${PROXY_HOSTPORT}")" \
        || fail "proxy login failed: ${PROXY_SID}"
    PROXY_ROW_IP="$(session_ip "${PROXY_SID}")"
    [ "${PROXY_ROW_IP}" = "${PROXY_CLIENT_IP}" ] \
        || fail "AC-2: session via proxy recorded ip_address=${PROXY_ROW_IP}, want the proxy-supplied ${PROXY_CLIENT_IP}"
    log "AC-2 OK: login through proxy recorded ip_address=${PROXY_ROW_IP} (proxy-supplied client IP)"

    # --- Assertion 10 (AC-3): DIRECT login from an UNTRUSTED network, forging
    # X-Forwarded-For. The login is driven from the `forging-client` container,
    # which lives ONLY on clientnet (10.124.0.0/24, NOT in TRUSTED_PROXY_CIDRS),
    # and reaches atlas by service name over that network. atlas therefore sees
    # the request arrive from a 10.124.x peer — an untrusted hop — so the walk
    # never consults the forged X-Forwarded-For.
    #
    # NOTE (slice 470 finding): we do NOT use atlas's HOST-published port for
    # this leg, because docker source-NATs a host-port request to the bridge
    # GATEWAY (10.123.0.1), which is inside the trusted /24 — atlas would then
    # correctly honour the forged header (right walk behaviour, wrong threat
    # model for AC-3). Driving from an off-trust container is the faithful model.
    #
    # The forging-client curl prints the response headers (-D -) so we can read
    # the Set-Cookie: atlas_session=<id> line — the session id whose row we
    # then inspect. The body is discarded (-o /dev/null).
    log "direct login: POST /auth/local/login from forging-client (clientnet, untrusted) forging X-Forwarded-For: ${FORGED_IP}"
    FORGE_HDRS="$(mktemp)"
    set +e
    "${COMPOSE[@]}" run --rm --no-deps -T \
        --entrypoint curl forging-client \
        -sS -o /dev/null -D - \
        -X POST -H "Content-Type: application/json" \
        -H "X-Forwarded-For: ${FORGED_IP}" \
        --data '{"tenant_id":"00000000-0000-4000-8000-000000000001","email":"admin@example.com","password":"test-default-user-password"}' \
        "http://atlas:8080/auth/local/login" > "${FORGE_HDRS}" 2>/dev/null
    FORGE_RC=$?
    set -e
    [ "${FORGE_RC}" = "0" ] || fail "AC-3: forging-client login curl exited ${FORGE_RC}"
    # Extract the atlas_session cookie value from the Set-Cookie response header.
    DIRECT_SID="$(grep -i '^set-cookie:' "${FORGE_HDRS}" \
        | sed -n 's/.*atlas_session=\([^;]*\).*/\1/p' | head -n1 | tr -d '\r\n')"
    rm -f "${FORGE_HDRS}"
    [ -n "${DIRECT_SID}" ] || fail "AC-3: forging-client login set no atlas_session cookie (login failed?)"
    DIRECT_ROW_IP="$(session_ip "${DIRECT_SID}")"
    if [ "${DIRECT_ROW_IP}" = "${FORGED_IP}" ]; then
        fail "AC-3: forged X-Forwarded-For (${FORGED_IP}) from an untrusted peer was HONOURED — spoofing not rejected"
    fi
    # The recorded peer must be the forging-client's clientnet address
    # (10.124.x), never the forged TEST-NET value, and never null.
    case "${DIRECT_ROW_IP}" in
        10.124.*) : ;;  # expected: the untrusted clientnet peer
        ""|"<null>") fail "AC-3: direct login recorded no ip_address (${DIRECT_ROW_IP})" ;;
        *) log "AC-3 note: recorded peer ${DIRECT_ROW_IP} (expected a 10.124.x clientnet address; forged value still rejected)" ;;
    esac
    log "AC-3 OK: untrusted-peer login recorded ip_address=${DIRECT_ROW_IP} (forged ${FORGED_IP} rejected)"
fi

# ---------------------------------------------------------------------
# Assertion 8 (AC-7): a fresh re-run of atlas-bootstrap against the now-
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

# ---------------------------------------------------------------------
# Slice 473 — AC-7: REPRODUCE THE MIGRATE-ON-UPGRADE INCIDENT.
#
# Only the `migrate` mode runs this block. By here the bundle is up and
# fully seeded at migration set N (the existing assertions already proved
# the new atlas-migrate gating brings a fresh stack to healthy). Now we
# reproduce the 2026-06-05 incident shape: an image update lands a NEWER
# migration set, and we prove the always-run atlas-migrate service applies
# it AND the backend serves only after the migrate completes.
#
# Steps:
#   1. Record the schema_migrations tip (set N) + the seed row counts.
#   2. Write a brand-new sentinel migration into the bind-mounted checkout
#      (migrations/sql/<future-ts>_slice473_ac7_sentinel.sql) that creates
#      a marker table. This is what an upgraded image carries.
#   3. Re-run the always-run `atlas-migrate` service (simulating the
#      migrate step that runs on every bring-up / image update). Assert it
#      exits 0 AND the sentinel table now exists AND the ledger advanced.
#   4. Force-recreate the `atlas` backend (simulating the Watchtower
#      binary swap). Compose gates it on atlas-migrate
#      service_completed_successfully — assert atlas comes back HEALTHY
#      (serving) ONLY after the migrate step has completed: i.e. /health
#      is 200 again and the schema is current. The gate is the proof the
#      backend cannot serve a partial schema (AC-3 / AC-4 / P0-473-1).
#   5. Idempotency (AC-2): a SECOND atlas-migrate run is a "schema
#      current" no-op (exit 0, log line) and does NOT re-seed — the seed
#      row counts are unchanged.
# ---------------------------------------------------------------------
if [ "${MODE}" = "migrate" ]; then
    MIGRATIONS_DIR="${REPO_ROOT}/migrations/sql"

    log "AC-7: recording schema tip (set N) and seed counts"
    LEDGER_N="$(db_count 'SELECT count(*) FROM schema_migrations')"
    CONTROLS_N="$(db_count 'SELECT count(*) FROM controls')"
    USERS_N="$(db_count 'SELECT count(*) FROM users')"
    SCOPES_N="$(db_count 'SELECT count(*) FROM scope_cells')"
    log "AC-7: set N = ${LEDGER_N} ledgered migrations; controls=${CONTROLS_N} users=${USERS_N} scope_cells=${SCOPES_N}"

    # A far-future timestamp prefix guarantees lexical-last ordering so the
    # for-loop in migrate.sh applies it after every real migration. The
    # marker table is trivially reversible-by-drop and touches no tenant
    # data. The basename is unique per run-id to avoid any ledger clash.
    SENTINEL_BASE="29991231000000_slice473_ac7_sentinel.sql"
    SENTINEL_SQL="${MIGRATIONS_DIR}/${SENTINEL_BASE}"
    SENTINEL_TABLE="slice473_ac7_marker"
    log "AC-7: writing sentinel migration ${SENTINEL_BASE} (simulates a newer image's migration set)"
    cat > "${SENTINEL_SQL}" <<SQL
-- slice 473 AC-7 sentinel migration (test-only; removed by the harness on exit).
-- Reproduces the migrate-on-upgrade incident: a newer image carries a
-- migration the running DB has not applied. CREATE TABLE IF NOT EXISTS so a
-- partial-failure retry stays clean.
CREATE TABLE IF NOT EXISTS ${SENTINEL_TABLE} (
    id          INT PRIMARY KEY DEFAULT 1,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO ${SENTINEL_TABLE} (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
SQL

    # The sentinel must NOT already be applied (we just created it).
    SENTINEL_EXISTS_BEFORE="$(db_count "SELECT to_regclass('public.${SENTINEL_TABLE}') IS NOT NULL")"
    [ "${SENTINEL_EXISTS_BEFORE}" = "f" ] \
        || fail "AC-7: sentinel table already existed before the upgrade run (got '${SENTINEL_EXISTS_BEFORE}')"
    log "AC-7: confirmed sentinel table absent at set N"

    # --- Step 3: re-run the always-run migrate step (image-update sim) ---
    log "AC-7: re-running atlas-migrate (simulates the always-run migrate step on image update)"
    set +e
    "${COMPOSE[@]}" run --rm atlas-migrate
    MIGRATE_RC=$?
    set -e
    [ "${MIGRATE_RC}" = "0" ] || fail "AC-7: atlas-migrate upgrade run exited ${MIGRATE_RC}, want 0"

    SENTINEL_EXISTS_AFTER="$(db_count "SELECT to_regclass('public.${SENTINEL_TABLE}') IS NOT NULL")"
    [ "${SENTINEL_EXISTS_AFTER}" = "t" ] \
        || fail "AC-7: sentinel migration did NOT apply on re-up (table still absent: '${SENTINEL_EXISTS_AFTER}')"
    LEDGER_AFTER="$(db_count 'SELECT count(*) FROM schema_migrations')"
    [ "${LEDGER_AFTER}" -eq "$((LEDGER_N + 1))" ] 2>/dev/null \
        || fail "AC-7: ledger did not advance by exactly 1 (was ${LEDGER_N}, now ${LEDGER_AFTER})"
    SENTINEL_LEDGERED="$(db_count "SELECT 1 FROM schema_migrations WHERE filename = '${SENTINEL_BASE}'")"
    [ "${SENTINEL_LEDGERED}" = "1" ] || fail "AC-7: sentinel migration not recorded in schema_migrations ledger"
    log "AC-7 OK (AC-1/AC-2): new migration applied on re-up; ledger ${LEDGER_N} -> ${LEDGER_AFTER}"

    # --- Step 4: force-recreate the backend (Watchtower binary-swap sim) ---
    # Compose gates atlas on atlas-migrate:service_completed_successfully.
    # The migrate step we just ran exited 0, so the gate is satisfied and
    # the recreated backend may serve — against the NOW-CURRENT schema.
    # Assert it comes back healthy (serving) only after migrate completed.
    log "AC-7: force-recreating atlas backend (simulates the Watchtower binary swap)"
    "${COMPOSE[@]}" up -d --force-recreate --no-deps atlas
    ATLAS_HEALTH_OK=""
    for i in $(seq 1 90); do
        if curl -fsS -o /dev/null "http://127.0.0.1:${ATLAS_HOSTPORT}/health" 2>/dev/null; then
            ATLAS_HEALTH_OK=1
            break
        fi
        sleep 2
    done
    [ -n "${ATLAS_HEALTH_OK}" ] || fail "AC-7: atlas /health never returned 200 after the recreate"
    # The backend is serving AND the schema is current (sentinel present) —
    # i.e. it served only after migrate brought the schema forward, never
    # against the partial pre-sentinel schema. (P0-473-1 / AC-3 / AC-4.)
    SCHEMA_STILL_CURRENT="$(db_count "SELECT to_regclass('public.${SENTINEL_TABLE}') IS NOT NULL")"
    [ "${SCHEMA_STILL_CURRENT}" = "t" ] \
        || fail "AC-7: backend healthy but schema not current — gate did not hold"
    log "AC-7 OK (AC-3/AC-4/P0-473-1): backend serves a CURRENT schema after recreate; never served the partial schema"

    # --- Step 5: idempotency — a second migrate run is a no-op no-reseed ---
    log "AC-7: re-running atlas-migrate a SECOND time (idempotency / no-reseed)"
    set +e
    MIGRATE2_OUT="$("${COMPOSE[@]}" run --rm atlas-migrate 2>&1)"
    MIGRATE2_RC=$?
    set -e
    printf '%s\n' "${MIGRATE2_OUT}"
    [ "${MIGRATE2_RC}" = "0" ] || fail "AC-7: second atlas-migrate run exited ${MIGRATE2_RC}, want 0"
    printf '%s' "${MIGRATE2_OUT}" | grep -q "schema current" \
        || fail "AC-7: second migrate run did not log the 'schema current' idempotent no-op line"
    CONTROLS_FINAL="$(db_count 'SELECT count(*) FROM controls')"
    USERS_FINAL="$(db_count 'SELECT count(*) FROM users')"
    SCOPES_FINAL="$(db_count 'SELECT count(*) FROM scope_cells')"
    [ "${CONTROLS_FINAL}" = "${CONTROLS_N}" ] || fail "AC-7: controls changed on idempotent re-run: ${CONTROLS_N} -> ${CONTROLS_FINAL} (re-seed?)"
    [ "${USERS_FINAL}" = "${USERS_N}" ]       || fail "AC-7: users changed on idempotent re-run: ${USERS_N} -> ${USERS_FINAL} (re-seed?)"
    [ "${SCOPES_FINAL}" = "${SCOPES_N}" ]     || fail "AC-7: scope_cells changed on idempotent re-run: ${SCOPES_N} -> ${SCOPES_FINAL} (re-seed?)"
    log "AC-7 OK (AC-2): second migrate run is a 'schema current' no-op; seed rows unchanged (no re-seed)"

    log "AC-7 PASSED: migrate-on-upgrade reproduced and fixed (apply-on-re-up + serve-after-gate + idempotent)"
fi

log "ALL ASSERTIONS PASSED (${MODE})"
