#!/bin/sh
# security-atlas — always-run, idempotent, fail-closed migrate step (slice 473).
#
# Runs as the `atlas-migrate[-edge]` compose service. It is the ONLY step
# that applies SQL migrations, and unlike the one-shot `atlas-bootstrap`
# seed/SCF/upload phases it runs on EVERY stack bring-up / image update —
# not only on first boot.
#
# WHY THIS EXISTS (confirmed production incident, 2026-06-05).
#
# The self-host stack used to apply migrations only inside the
# `atlas-bootstrap[-edge]` one-shot first-boot container. Two compose
# facts made that a silent footgun on upgrade:
#
#   1. Watchtower auto-updates the `atlas[-edge]` BINARY (it carries the
#      watchtower-enable label) but NOT the bootstrap/migrate one-shot
#      (no label, restart:"no"). So an image update landed a newer binary
#      on an older schema.
#   2. The backend `depends_on` the bootstrap with
#      `condition: service_started`, NOT `service_completed_successfully`,
#      so nothing gated serving on a completed migrate.
#
# Result: the maintainer's atlas-edge box silently fell 3 migrations
# behind after an image update, and the demo-seed button later failed
# with a masked HTTP 500 (the `me_audit_log_action_check` CHECK rejected
# the `demo_seed` action value that the un-applied migration would have
# added). The drift surfaced HOURS later as a runtime "action failed",
# not at deploy time.
#
# THE FIX (slice 473). This script is split out of bootstrap.sh's old
# phases 1-2 and wired as a dedicated `atlas-migrate` service that:
#
#   * runs on every bring-up (no first-boot-only gating);
#   * is idempotent — an already-current DB applies nothing and exits 0
#     with a "schema current" log line (no error, no re-seed);
#   * fails CLOSED — a migration failure exits non-zero with a log line
#     naming the failing migration filename + the SQL error, and the
#     backend (gated on this service via `service_completed_successfully`)
#     does NOT start against a partial schema;
#   * runs as `atlas_migrate` (existing DATABASE_URL[_MIGRATE]) — no
#     privilege widening, no superuser, no down-migrations.
#
# The seed / SCF import / control-bundle upload stay in bootstrap.sh as
# first-boot-only steps; ONLY this migrate step became always-run.
#
# Required env (set by docker-compose[.edge].yml from .env):
#   DATABASE_URL        atlas_migrate connection string (BYPASSRLS)
#   ATLAS_APP_PASSWORD  (optional) sets atlas_app's password idempotently
#   REPO_ROOT           defaults to /repo (migrations baked into the image)
#
# This script connects to Postgres ONLY as atlas_migrate (DATABASE_URL =
# DATABASE_URL_MIGRATE) — the only context allowed to write DDL across the
# RLS boundary. It never runs as a superuser and never widens the role.

set -eu

REPO_ROOT="${REPO_ROOT:-/repo}"

log() { echo "[migrate] $*"; }

# ----- Phase 1: wait for Postgres -----
log "waiting for Postgres..."
i=0
until psql "$DATABASE_URL" -c 'SELECT 1' >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 60 ]; then
        log "FATAL: Postgres not reachable after 60 attempts"
        exit 1
    fi
    sleep 2
done
log "Postgres reachable"

# ----- Phase 2a: bootstrap roles -----
# 01-roles.sql is fully IF-NOT-EXISTS-guarded, so re-running it as
# atlas_migrate on every bring-up stays a clean no-op. On a fresh bundled
# volume the postgres image's /docker-entrypoint-initdb.d already ran it
# once as the superuser; this re-run keeps the shared-cluster path warm.
log "applying bootstrap roles (idempotent)..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$REPO_ROOT/migrations/bootstrap/01-roles.sql"

# ----- Phase 2b: schema_migrations ledger -----
# Forward migrations are tracked filename-by-filename in a plain
# unversioned `schema_migrations` ledger (slice 065 bug #3). The ledger is
# what makes re-runs safe — the migration files themselves are NOT blanket
# IF-NOT-EXISTS-guarded; we record each applied basename and skip it on
# re-run. (The CREATE TYPE statements inside the migrations ARE
# individually guarded so a migration that failed mid-apply can retry.)
#
# schema_migrations carries no tenant_id and no RLS — it is operational
# metadata owned by atlas_migrate, same category as any versioning tool's
# bookkeeping table.
log "ensuring schema_migrations ledger..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "
    CREATE TABLE IF NOT EXISTS schema_migrations (
        filename    TEXT PRIMARY KEY,
        applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
    );
"

# ----- Phase 2c: apply forward migrations (fail-closed) -----
log "applying forward migrations..."
applied=0
for f in "$REPO_ROOT"/migrations/sql/*.sql; do
    case "$f" in
        *.down.sql) ;;  # never auto-run down-migrations (P0-473-2)
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
            #
            # FAIL-CLOSED (P0-473-1 / P0-473-5): if psql exits non-zero the
            # migration failed; emit a FATAL line NAMING the failing file +
            # the SQL error (psql already printed the SQL error to stderr
            # above), then exit non-zero. The backend is gated on this
            # service via `service_completed_successfully`, so it will NOT
            # start against the partial schema.
            if ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 --single-transaction \
                -f "$f" \
                -c "INSERT INTO schema_migrations (filename) VALUES ('$base')"; then
                log "FATAL: migration '$base' failed to apply — see the SQL error above."
                log "FATAL: the schema is NOT advanced past this migration; the atlas"
                log "FATAL: backend is gated on this step and will NOT serve against a"
                log "FATAL: partial schema. Fix the migration and re-run the stack."
                exit 1
            fi
            applied=$((applied + 1))
            ;;
    esac
done

if [ "$applied" -eq 0 ]; then
    log "schema current — no migrations to apply (idempotent no-op)"
else
    log "applied $applied migration(s)"
fi

# ----- Phase 2d: set the atlas_app role password -----
# The application role needs a password so the atlas server (which
# connects as atlas_app via DATABASE_URL_APP) can authenticate. This is a
# DDL-role concern and must complete before the backend tries to connect,
# so it lives here in the migrate step (gated ahead of the backend), not
# in the seed one-shot. ALTER ROLE ... PASSWORD is idempotent.
if [ -n "${ATLAS_APP_PASSWORD:-}" ]; then
    log "setting atlas_app role password..."
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
        -c "ALTER ROLE atlas_app PASSWORD '$ATLAS_APP_PASSWORD'"
fi

log "migrate complete — schema is current"
