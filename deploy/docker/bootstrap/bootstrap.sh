#!/bin/sh
# security-atlas — docker-compose self-host first-boot bootstrap (slice 037).
#
# Runs as the one-shot `atlas-bootstrap` compose service. It is the
# integration glue that makes the bundle "installable + seeded": it turns
# a freshly-migrated Postgres into a usable security-atlas deployment.
#
# Slice 473: SQL migrations are NO LONGER applied here. They moved to the
# dedicated always-run `atlas-migrate[-edge]` service (migrate.sh), which
# both this bootstrap and the atlas backend `depends_on` with
# `condition: service_completed_successfully`. So by the time this script
# runs, the schema is already current AND the atlas_app password is set.
# This script does ONLY the first-boot-only steps:
#
#   1. wait for Postgres to accept connections
#   2. seed: default tenant + builtin scope dimension + default scope cell
#      + default local user (argon2id password hash)
#   3. import the SCF catalog
#   4. wait for the atlas server's /health to return 200
#   5. ensure an OAuth client + upload the 50 SOC 2 control bundles
#
# Why the split (confirmed production incident, 2026-06-05): the old
# bootstrap was the ONLY migrate path AND was one-shot/un-labelled, so
# Watchtower advanced the binary while the migrate step never re-ran —
# the binary served against a stale schema and a masked HTTP 500 surfaced
# hours later. migrate.sh's header documents the incident in full.
#
# Idempotent: every phase is safe to re-run. seed.sql uses ON CONFLICT
# DO NOTHING; SCF import and control upload both upsert. So
# `docker compose up` after a restart re-runs this container and it
# exits 0 without duplicating anything (the migrate-on-upgrade footgun
# this slice fixed lived in the MIGRATE step, which is now its own
# always-run service — re-running THIS bootstrap stays a one-shot seed
# no-op).
#
# Required env (set by docker-compose.yml from .env / .env.example):
#   DATABASE_URL                 atlas_migrate connection string (BYPASSRLS)
#   ATLAS_HTTP_URL               e.g. http://atlas:8080
#   ATLAS_BOOTSTRAP_TENANT       default tenant UUID
#   ATLAS_DEFAULT_USER_EMAIL     default local sign-in email
#   ATLAS_DEFAULT_USER_PASSWORD  default local sign-in password
#
# Slice 473: ATLAS_APP_PASSWORD is consumed by migrate.sh (it sets the
# atlas_app role password as part of the DDL-role migrate step), not here.
#
# Slice 196: ATLAS_BOOTSTRAP_TOKEN is no longer consumed by this script.
# Phase 5 issues an OAuth client at runtime via `atlas-cli oauth
# issue-client`, persists credentials to
# ${ATLAS_DATA_DIR}/oauth-bootstrap-credentials.json (mode 0600), and
# drives `atlas-cli controls upload --client-id ... --client-secret ...`.
# The atlas service still consumes ATLAS_BOOTSTRAP_TOKEN to mint the
# slice-037 fixed-token admin credential (AC-4 transitional — keeps
# the legacy operator path warm; spillover slice will retire it).
#
# This script connects to Postgres as atlas_migrate (BYPASSRLS) — the only
# context allowed to write across the RLS boundary during bootstrap.

set -eu

BOOTSTRAP_DIR="$(dirname "$0")"
REPO_ROOT="${REPO_ROOT:-/repo}"
SCF_CATALOG="${SCF_CATALOG:-$REPO_ROOT/migrations/fixtures/scf-sample.json}"
CONTROLS_DIR="${CONTROLS_DIR:-$REPO_ROOT/controls/soc2}"

log() { echo "[bootstrap] $*"; }

# ----- Phase 1: wait for Postgres -----
#
# Slice 473: this bootstrap is gated on `atlas-migrate` via
# `service_completed_successfully`, so by the time it runs the schema is
# already current (roles created, all forward migrations applied,
# atlas_app password set). We still wait for Postgres to accept our own
# connection before seeding — the migrate gate guarantees the schema, the
# wait-loop just covers the (brief) window between migrate exiting and
# Postgres accepting this fresh connection.
log "waiting for Postgres..."
i=0
until psql "$DATABASE_URL" -c 'SELECT 1' >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 60 ]; then
        log "ERROR: Postgres not reachable after 60 attempts"
        exit 1
    fi
    sleep 2
done
log "Postgres reachable"

# Slice 473: migrations + roles + atlas_app password moved to migrate.sh
# (the always-run, fail-closed `atlas-migrate` service). They are NOT
# re-applied here — the `service_completed_successfully` gate guarantees
# the schema is current before this seed step runs.

# ----- Phase 2: seed default tenant / scope / user -----
log "generating argon2id hash for the default user password..."
DEFAULT_USER_HASH="$(printf '%s\n' "$ATLAS_DEFAULT_USER_PASSWORD" | atlas-cli bootstrap hash-password)"

log "seeding default tenant + scope + user..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    -v default_tenant_id="$ATLAS_BOOTSTRAP_TENANT" \
    -v default_user_email="$ATLAS_DEFAULT_USER_EMAIL" \
    -v default_user_password_hash="$DEFAULT_USER_HASH" \
    -f "$BOOTSTRAP_DIR/seed.sql"

# ----- Phase 3: import the SCF catalog -----
log "importing SCF catalog from $SCF_CATALOG ..."
atlas-cli catalog import-scf "$SCF_CATALOG"

# ----- Phase 4: wait for the atlas server /health -----
log "waiting for atlas /health at $ATLAS_HTTP_URL/health ..."
i=0
until wget -q -O /dev/null "$ATLAS_HTTP_URL/health" 2>/dev/null; do
    i=$((i + 1))
    if [ "$i" -ge 90 ]; then
        log "ERROR: atlas /health not 200 after 90 attempts"
        exit 1
    fi
    sleep 2
done
log "atlas /health is up"

# ----- Phase 5a: ensure an OAuth client for bundle upload -----
#
# Slice 196 migrates this bootstrap step off the pre-shared
# ATLAS_BOOTSTRAP_TOKEN onto OAuth client_credentials. The client is
# issued ONCE per deployment + persisted to a 0600 file under
# ${ATLAS_DATA_DIR}; subsequent re-runs of this container reuse the
# persisted credentials.
#
# Uniqueness (P0-196-3): the client name carries an 8-hex-char
# fingerprint derived from ATLAS_BOOTSTRAP_TENANT so multi-instance
# docker-compose runs with distinct tenants get distinct client names
# (avoiding ErrDuplicateName collisions across deployments).
# Single-deployment re-runs reuse the persisted file before ever
# calling oauth issue-client.
ATLAS_DATA_DIR="${ATLAS_DATA_DIR:-/var/lib/atlas-bootstrap}"
OAUTH_CREDS_FILE="${ATLAS_DATA_DIR}/oauth-bootstrap-credentials.json"
TENANT_SHORT="$(printf '%s' "$ATLAS_BOOTSTRAP_TENANT" | tr -d -- '-' | cut -c1-8)"
OAUTH_CLIENT_NAME="atlas-bootstrap-controls-${TENANT_SHORT}"

mkdir -p "$ATLAS_DATA_DIR"
chmod 0700 "$ATLAS_DATA_DIR" 2>/dev/null || true

if [ -s "$OAUTH_CREDS_FILE" ]; then
    log "reusing persisted OAuth bootstrap credentials at $OAUTH_CREDS_FILE"
else
    log "issuing OAuth bootstrap client '$OAUTH_CLIENT_NAME' ..."
    # `oauth issue-client` prints two lines to stdout:
    #   client_id: <uuid>
    #   client_secret: <plaintext>
    # Capture, parse, persist. If the client already exists in the DB
    # (ErrDuplicateName — credentials file was wiped but DB row remains),
    # fall back to a unix-second-suffixed retry name so bootstrap stays
    # idempotent against operator volume wipes.
    set +e
    ISSUE_OUT="$(atlas-cli oauth issue-client "$OAUTH_CLIENT_NAME" 2>&1)"
    ISSUE_RC=$?
    set -e
    if [ "$ISSUE_RC" -ne 0 ]; then
        case "$ISSUE_OUT" in
            *"already exists"*)
                RETRY_NAME="${OAUTH_CLIENT_NAME}-retry-$(date -u +%s)"
                log "  '$OAUTH_CLIENT_NAME' already exists — issuing '$RETRY_NAME' instead"
                ISSUE_OUT="$(atlas-cli oauth issue-client "$RETRY_NAME")"
                OAUTH_CLIENT_NAME="$RETRY_NAME"
                ;;
            *)
                log "ERROR: oauth issue-client failed: $ISSUE_OUT"
                exit 1
                ;;
        esac
    fi
    OAUTH_CLIENT_ID="$(printf '%s\n' "$ISSUE_OUT" | sed -n 's/^client_id: //p')"
    OAUTH_CLIENT_SECRET="$(printf '%s\n' "$ISSUE_OUT" | sed -n 's/^client_secret: //p')"
    if [ -z "$OAUTH_CLIENT_ID" ] || [ -z "$OAUTH_CLIENT_SECRET" ]; then
        log "ERROR: failed to parse client_id / client_secret from issue-client output"
        exit 1
    fi
    # Persist with mode 0600 BEFORE writing content so the secret never
    # lives in a world-readable file for any window.
    (umask 0177 && printf '{"client_id":"%s","client_secret":"%s","name":"%s"}\n' \
        "$OAUTH_CLIENT_ID" "$OAUTH_CLIENT_SECRET" "$OAUTH_CLIENT_NAME" \
        > "$OAUTH_CREDS_FILE")
    chmod 0600 "$OAUTH_CREDS_FILE"
    log "persisted OAuth bootstrap credentials to $OAUTH_CREDS_FILE (mode 0600)"
fi

# Read credentials (always — both first-run and reuse paths land here).
OAUTH_CLIENT_ID="$(sed -n 's/.*"client_id":"\([^"]*\)".*/\1/p' "$OAUTH_CREDS_FILE")"
OAUTH_CLIENT_SECRET="$(sed -n 's/.*"client_secret":"\([^"]*\)".*/\1/p' "$OAUTH_CREDS_FILE")"
if [ -z "$OAUTH_CLIENT_ID" ] || [ -z "$OAUTH_CLIENT_SECRET" ]; then
    log "ERROR: persisted credentials file at $OAUTH_CREDS_FILE missing client_id/secret"
    exit 1
fi

# ----- Phase 5b: upload the 50 SOC 2 control bundles -----
log "uploading control bundles from $CONTROLS_DIR ..."
uploaded=0
for dir in "$CONTROLS_DIR"/*/; do
    [ -f "$dir/control.yaml" ] || continue
    atlas-cli controls upload "$dir" \
        --endpoint "$ATLAS_HTTP_URL" \
        --issuer "$ATLAS_HTTP_URL" \
        --client-id "$OAUTH_CLIENT_ID" \
        --client-secret "$OAUTH_CLIENT_SECRET"
    uploaded=$((uploaded + 1))
done
log "uploaded $uploaded control bundles"

# Slice 073: emit the grep-friendly bootstrap-token line + write the
# 0600 file (the platform also writes the file in cmd/atlas; this is
# triple redundancy so the operator can find the token via three
# orthogonal paths: stderr-of-atlas, `docker compose logs atlas | grep
# BOOTSTRAP_TOKEN`, or filesystem inspection at
# ${ATLAS_DATA_DIR}/bootstrap-token). The file is atomically deleted
# by atlas on the first successful sign-in (load-bearing P0-A1 safety
# property: long-lived bootstrap tokens on disk are a credential leak
# shape this slice does not introduce).
#
# Slice 196: ATLAS_BOOTSTRAP_TOKEN is no longer wired into the
# atlas-bootstrap service env in docker-compose.yml — the upload path
# moved to OAuth client_credentials above. The block below stays only
# as a backwards-compat null path: if an operator points a legacy
# bootstrap that still passes ATLAS_BOOTSTRAP_TOKEN at this script
# (e.g., a Helm chart that hasn't been re-templated yet), the
# grep-line + file write still happen — the slice-037 fixed-token
# legacy mint at cmd/atlas/main.go also continues to work.
if [ -n "${ATLAS_BOOTSTRAP_TOKEN:-}" ]; then
    echo "ATLAS_BOOTSTRAP_TOKEN=${ATLAS_BOOTSTRAP_TOKEN}  # one-time use, see docs-site/docs/troubleshooting/first-login.md"
    TOKEN_FILE="${ATLAS_DATA_DIR:-/var/lib/atlas}/bootstrap-token"
    TOKEN_DIR="$(dirname "$TOKEN_FILE")"
    if mkdir -p "$TOKEN_DIR" 2>/dev/null; then
        printf '%s' "$ATLAS_BOOTSTRAP_TOKEN" > "$TOKEN_FILE" 2>/dev/null \
            && chmod 0600 "$TOKEN_FILE" 2>/dev/null \
            && log "bootstrap-token file written at $TOKEN_FILE (mode 0600)"
    fi
    log "first-time sign-in: see ${TOKEN_FILE} or 'docker compose logs atlas-bootstrap | grep BOOTSTRAP_TOKEN'"
fi

log "bootstrap complete — security-atlas is seeded and ready"
