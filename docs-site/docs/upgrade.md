# Upgrade

This runbook is the operator procedure for upgrading a self-hosted
security-atlas deployment to a newer release: pin a version, take a
pre-upgrade backup checkpoint, apply migrations, verify, and roll back
if the upgrade goes wrong.

It pairs with [Backup and restore](backup-restore.md) — the checkpoint
in step 2 _is_ a backup, and the rollback path _is_ a restore. It also
operationalizes the continuity posture in the
[Business continuity & disaster recovery plan](https://github.com/mgoodric/security-atlas/blob/main/docs/governance/business-continuity.md):
an upgrade that bricks the database is a Scenario B (Postgres
corruption) event, and the recovery is the same restore-from-checkpoint
path.

<!-- prettier-ignore-start -->
!!! warning "Never upgrade without a checkpoint"
    The single rule that makes upgrades safe: **take a backup checkpoint
    before you migrate.** A forward-only migration cannot be undone in
    place; the checkpoint is the only rollback that always works.
<!-- prettier-ignore-end -->

---

## Before you start

| Decision                | Recommendation                                                                                                                                                                                                                     |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **What version to run** | Pin a specific tag, not `:latest`, in production (see [Pin a version](#1-pin-a-version))                                                                                                                                           |
| **When migrations run** | **Automatically on every bring-up, fail-closed** (slice 473) — the `atlas-migrate` service runs first and the backend will not serve until it completes. You may still run them explicitly before bringing the server up (step 3). |
| **Backup checkpoint**   | Always — Postgres dump + artifact-store mirror, encrypted, offsite                                                                                                                                                                 |
| **Breaking changes**    | Read the release's `CHANGELOG.md` entry; breaking changes carry a documented upgrade path                                                                                                                                          |

---

## 1. Pin a version

security-atlas publishes a container image to
`ghcr.io/mgoodric/security-atlas` on every release tag. For production,
pin to a **minor** tag until you have run a few upgrade cycles and trust
the release pipeline; this gets you patches without an unattended jump
to a new minor.

| Tag pattern      | Behavior                                               | Use when                                                        |
| ---------------- | ------------------------------------------------------ | --------------------------------------------------------------- |
| `:latest`        | Every release auto-applies                             | You trust the release discipline and run non-production         |
| `:0.3` (minor)   | Auto-updates within `0.3.x`, never auto-jumps to `0.4` | **Recommended for production** — patches yes, new minors opt-in |
| `:0.3.5` (patch) | Fully manual; no auto-update                           | You want complete control over every upgrade                    |

Set the tag in your `.env` / compose override and keep it under version
control alongside the rest of your deployment config.

<!-- prettier-ignore-start -->
!!! note "Postgres is deliberately not auto-updated"
    In the [Watchtower auto-update pattern](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md#watchtower-opt-in-auto-update-from-ghcr),
    only the `atlas` container carries the auto-update label. **Postgres
    is intentionally never auto-updated** — a major Postgres version
    bump needs a manual `pg_dump` + restore, and letting an updater
    swap the Postgres image underneath a live data volume can brick the
    database. Major Postgres upgrades are a deliberate, manual,
    checkpoint-first operation.
<!-- prettier-ignore-end -->

---

## 2. Take a pre-upgrade backup checkpoint

This is the rollback insurance. Follow [Backup and restore](backup-restore.md)
in full; the minimum checkpoint is:

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"
STAMP="$(date -u +%Y-%m-%dT%H%M%SZ)"

# Postgres dump (the rollback substrate).
$COMPOSE exec -T postgres \
  pg_dump -U postgres -d security_atlas --no-owner \
  | gzip -c > "pre-upgrade-${STAMP}.sql.gz"
shasum -a 256 "pre-upgrade-${STAMP}.sql.gz" > "pre-upgrade-${STAMP}.sql.gz.sha256"

# Encrypt + push offsite (see Backup and restore for the age/rclone step).
# Mirror the artifact store too if you run MinIO.
```

Confirm the checkpoint verifies (`shasum -a 256 -c ...`) **before** you
touch the running deployment. A checkpoint you have not verified is not
a checkpoint.

---

## 3. Apply the new release

**Migrations run automatically, fail-closed, on every bring-up (slice
473).** The bundle ships a dedicated `atlas-migrate` service: it applies
every `migrations/sql/*.sql` not yet recorded in the `schema_migrations`
ledger, in a single transaction per file, as the `atlas_migrate` role
(`BYPASSRLS`, no superuser). It is idempotent (an already-current
database applies nothing and exits 0) and **fail-closed** — a failing
migration exits non-zero naming the file, and the `atlas` backend, gated
on `atlas-migrate` via `service_completed_successfully`, **does not
serve** against a partial schema.

Because `atlas-migrate` carries the same Watchtower auto-update label as
the `atlas` binary, a Watchtower-driven image update advances the migrate
step in lockstep and the backend waits for it — a binary can never end up
newer than its schema.

You can simply bring the stack up and let the migrate step run:

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# 1. Pull the new images at your pinned tag.
$COMPOSE pull

# 2. Bring the stack up. atlas-migrate runs first (applies pending
#    migrations); atlas waits for it to complete, then serves.
$COMPOSE up -d
```

For a controlled production upgrade you may still apply migrations
**explicitly before** the server takes traffic — same idempotent runner,
just invoked on its own:

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# 1. Pull the new images at your pinned tag.
$COMPOSE pull

# 2. Stop the application so the schema is not changing under live traffic.
$COMPOSE stop atlas web

# 3. Apply forward migrations explicitly (idempotent; runs as atlas_migrate).
#    The atlas-migrate one-shot is the migration runner: it applies any
#    new migrations/sql/*.sql and records them in schema_migrations.
$COMPOSE run --rm atlas-migrate

# 4. Bring the new server up. It reconnects as atlas_app (RLS-enforced).
$COMPOSE up -d atlas web
```

<!-- prettier-ignore-start -->
!!! note "Migration mechanism — reconciling the guidance"
    The migration runner for the bundle is the dedicated, always-run
    `atlas-migrate` service (slice 473): it ledgers each file in
    `schema_migrations` and is safe to re-run, runs on every bring-up,
    and **fail-closed gates the backend** so a bad migration cannot let
    a newer binary serve a stale schema. Earlier docs referred to the
    `atlas-bootstrap` one-shot as the migration runner — that step now
    does seed / SCF import / control upload only (first-boot), and the
    migrate step is its own service. The platform binary still does NOT
    expose a `migrate` subcommand (this reconciles the slice-464
    SELF_HOSTING migrate-command drift). If you drive a bare Postgres
    outside the bundle, `just migrate-up` runs the same
    `migrations/sql/*.sql` set via `psql`.
<!-- prettier-ignore-end -->

---

## 4. Verify the upgrade

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# Health is green on the new version.
curl -fsS http://localhost:8080/health        # {"status":"ok","db":"ok"}

# The running binary is the version you pinned.
$COMPOSE exec -T atlas /usr/local/bin/atlas --version

# RLS policies are intact after the migration.
$COMPOSE exec -T postgres psql -U atlas_migrate -d security_atlas \
  -c "select count(*) from pg_policies where schemaname = 'public';"

# A signed OSCAL export still verifies — proves the data behind an
# export survived the migration intact (see Backup and restore, drill).
atlas oscal-export --period <frozen-period-id> --out ./post-upgrade-bundle
atlas oscal verify ./post-upgrade-bundle       # expect: signature OK
```

If `/health` reports `db` not-ok, or the version is wrong, or a verify
fails, treat the upgrade as failed and roll back.

---

## 5. Roll back

A failed upgrade rolls back by restoring the checkpoint from step 2.
Forward migrations are not reversible in place — the `.down.sql` files
exist for development, but the **safe production rollback is
restore-from-checkpoint**, which returns both schema and data to the
known-good pre-upgrade state.

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# 1. Stop the application.
$COMPOSE stop atlas web

# 2. Verify the checkpoint, then restore it as atlas_migrate (role-correct).
shasum -a 256 -c "pre-upgrade-STAMP.sql.gz.sha256"   # expect: OK
gunzip -c "pre-upgrade-STAMP.sql.gz" \
  | $COMPOSE exec -T postgres psql -U atlas_migrate -d security_atlas

# 3. Re-pin the PREVIOUS image tag in .env / your compose override.

# 4. Pull the previous image and bring the old version back up.
$COMPOSE pull
$COMPOSE up -d atlas web

# 5. Verify the rollback (same checks as step 4).
curl -fsS http://localhost:8080/health
```

<!-- prettier-ignore-start -->
!!! danger "Restore into a clean database"
    If the failed migration left the schema in a half-applied state,
    drop and recreate the database (or wipe the volume on a scratch
    target) before restoring, so the checkpoint restores into a clean
    schema rather than layering on top of a partial migration. See the
    [restore section](backup-restore.md#postgres-restore) of the backup
    runbook. The roles (`atlas_migrate`, `atlas_app`,
    `atlas_service_account`) are recreated by
    `migrations/bootstrap/01-roles.sql` at cluster init.
<!-- prettier-ignore-end -->

Record the failed upgrade and the rollback in your operational log per
the [BCP/DR plan §10](https://github.com/mgoodric/security-atlas/blob/main/docs/governance/business-continuity.md)
— a rollback is a continuity event.

---

## Major Postgres version upgrades

A **minor** Postgres patch (e.g. `16.4` → `16.5`) is an image bump
handled by re-pulling. A **major** Postgres upgrade (e.g. `16` → `17`)
is **not** an in-place image swap — the on-disk data format differs
between major versions. The path is:

1. Take a full `pg_dump` checkpoint (step 2).
2. Bring the stack down; remove the old Postgres data volume.
3. Change the Postgres image to the new major version.
4. Bring Postgres up fresh; restore the dump as `atlas_migrate`.
5. Bring `atlas` + `web` up; verify (step 4).

This is the dump-and-restore the self-host guide refers to when it says
Postgres is deliberately not auto-updated. Plan it as a maintenance
window, not an unattended update.

---

## See also

- [Backup and restore](backup-restore.md) — the checkpoint and rollback substrate
- [Install](install.md) — the self-host bring-up this upgrades
- [BCP/DR plan](https://github.com/mgoodric/security-atlas/blob/main/docs/governance/business-continuity.md) — RTO/RPO tiers and restore scenarios
