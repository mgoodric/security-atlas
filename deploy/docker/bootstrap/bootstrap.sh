#!/bin/sh
# security-atlas — docker-compose self-host first-boot bootstrap (slice 037).
#
# Runs as the one-shot `atlas-bootstrap` compose service. It is the
# integration glue that makes the bundle "installable + seeded": it turns
# an empty Postgres volume into a usable security-atlas deployment.
#
# Phases:
#   1. wait for Postgres to accept connections
#   2. apply migrations/bootstrap/01-roles.sql + migrations/sql/*.sql
#   3. seed: default tenant + builtin scope dimension + default scope cell
#      + default local user (argon2id password hash)
#   4. import the SCF catalog
#   5. wait for the atlas server's /health to return 200
#   6. upload the 50 SOC 2 control bundles
#
# Idempotent: every phase is safe to re-run. Forward migrations are
# tracked in a `schema_migrations` ledger and skipped once applied
# (slice 065 bug #3 / AC-7) — the migration files themselves are NOT
# blanket-guarded with IF NOT EXISTS, the ledger is what makes re-runs
# safe; the CREATE TYPE statements inside them ARE individually guarded
# so a migration that failed mid-apply can be retried. seed.sql uses
# ON CONFLICT DO NOTHING; SCF import and control upload both upsert. So
# `docker compose up` after a restart re-runs this container and it
# exits 0 without duplicating anything or erroring on already-applied
# DDL.
#
# Required env (set by docker-compose.yml from .env / .env.example):
#   DATABASE_URL                 atlas_migrate connection string (BYPASSRLS)
#   ATLAS_HTTP_URL               e.g. http://atlas:8080
#   ATLAS_BOOTSTRAP_TENANT       default tenant UUID
#   ATLAS_BOOTSTRAP_TOKEN        pre-shared admin token (matches atlas env)
#   ATLAS_DEFAULT_USER_EMAIL     default local sign-in email
#   ATLAS_DEFAULT_USER_PASSWORD  default local sign-in password
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

# ----- Phase 2: migrations -----
log "applying bootstrap roles..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$REPO_ROOT/migrations/bootstrap/01-roles.sql"

# Forward migrations are applied through a small `schema_migrations`
# ledger (slice 065 bug #3 / AC-7). The repo has no versioning tool —
# 01-roles.sql is run once and the `*.sql` files were originally just
# `psql -f`'d in a loop on every boot. That is NOT idempotent: a second
# `docker compose up` re-applies every file and the first unguarded
# `CREATE TABLE` aborts the bootstrap, stranding the deployment.
#
# The fix is a ledger, not blanket `IF NOT EXISTS` across 31 migration
# files: `_init.sql` is the sqlc source-of-truth and must stay clean, and
# guarding every CREATE TABLE / ADD COLUMN / CREATE INDEX / CREATE POLICY
# would be a large, fragile diff. Instead we record each applied
# migration's basename and skip it on re-run. The CREATE TYPE statements
# inside the migrations ARE still individually guarded (bug #3) so the
# partial-failure recovery path — a migration that errored AFTER creating
# its enums but BEFORE the ledger row was written — can re-run cleanly.
#
# `schema_migrations` is a plain unversioned table owned by atlas_migrate;
# it carries no tenant_id and no RLS (it is operational metadata, not
# tenant data — same category as a versioning tool's bookkeeping table).
log "ensuring schema_migrations ledger..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "
    CREATE TABLE IF NOT EXISTS schema_migrations (
        filename    TEXT PRIMARY KEY,
        applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
    );
"

log "applying forward migrations..."
for f in "$REPO_ROOT"/migrations/sql/*.sql; do
    case "$f" in
        *.down.sql) ;;
        *)
            base="$(basename "$f")"
            already="$(psql "$DATABASE_URL" -t -A \
                -c "SELECT 1 FROM schema_migrations WHERE filename = '$base'")"
            if [ "$already" = "1" ]; then
                log "  skipping $base (already applied)"
                continue
            fi
            log "  applying $base"
            # Apply the migration and record the ledger row in ONE psql
            # invocation wrapped in a transaction, so a migration that
            # fails partway leaves NO ledger row and is retried next run.
            psql "$DATABASE_URL" -v ON_ERROR_STOP=1 --single-transaction \
                -f "$f" \
                -c "INSERT INTO schema_migrations (filename) VALUES ('$base')"
            ;;
    esac
done

# The application role needs a password so the atlas server (which
# connects as atlas_app via DATABASE_URL_APP) can authenticate. Set it
# from ATLAS_APP_PASSWORD — idempotent (ALTER ROLE ... PASSWORD is a
# no-op-equivalent on re-run).
if [ -n "${ATLAS_APP_PASSWORD:-}" ]; then
    log "setting atlas_app role password..."
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
        -c "ALTER ROLE atlas_app PASSWORD '$ATLAS_APP_PASSWORD'"
fi

# ----- Phase 3: seed default tenant / scope / user -----
log "generating argon2id hash for the default user password..."
DEFAULT_USER_HASH="$(printf '%s\n' "$ATLAS_DEFAULT_USER_PASSWORD" | atlas-cli bootstrap hash-password)"

log "seeding default tenant + scope + user..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    -v default_tenant_id="$ATLAS_BOOTSTRAP_TENANT" \
    -v default_user_email="$ATLAS_DEFAULT_USER_EMAIL" \
    -v default_user_password_hash="$DEFAULT_USER_HASH" \
    -f "$BOOTSTRAP_DIR/seed.sql"

# ----- Phase 4: import the SCF catalog -----
log "importing SCF catalog from $SCF_CATALOG ..."
atlas-cli catalog import-scf "$SCF_CATALOG"

# ----- Phase 5: wait for the atlas server /health -----
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

# ----- Phase 6: upload the 50 SOC 2 control bundles -----
log "uploading control bundles from $CONTROLS_DIR ..."
uploaded=0
for dir in "$CONTROLS_DIR"/*/; do
    [ -f "$dir/control.yaml" ] || continue
    atlas-cli controls upload "$dir" \
        --endpoint "$ATLAS_HTTP_URL" \
        --token "$ATLAS_BOOTSTRAP_TOKEN"
    uploaded=$((uploaded + 1))
done
log "uploaded $uploaded control bundles"

log "bootstrap complete — security-atlas is seeded and ready"
