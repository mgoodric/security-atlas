# Self-hosting security-atlas

This guide covers the most common self-host path: one Docker host running the platform, Postgres, and an artifact store, with optional Watchtower-driven auto-update from GHCR on every tagged release.

The target is the same primary persona as the platform itself — the solo security leader at a 50–150-person security-product startup. The deployment should fit on one mid-size VM (4 vCPU, 16 GB RAM, 200 GB SSD), survive a reboot, and require no consulting hours.

---

## Prerequisites

- A Docker host with Docker Engine 24+ and `docker compose` plugin
- Outbound HTTPS to `ghcr.io` (for image pulls) and to any cloud provider whose APIs you connect (AWS, GitHub, etc.)
- A DNS name pointing at the host if you intend to expose the UI publicly
- An S3-compatible artifact store. Options:
  - **MinIO** — runs on the same host (simplest)
  - **Backblaze B2 / Wasabi / AWS S3** — managed; cheap if your evidence volume is modest

---

## Quick start — the full self-host bundle (recommended)

The `deploy/docker/docker-compose.yml` bundle brings up the **whole
platform** on one host — Postgres, NATS JetStream, MinIO, the `atlas`
server, and the Next.js frontend — and seeds it on first boot
(migrations, default tenant/scope/user, the SCF catalog, and the 50 SOC 2
control bundles). It is the bundle behind the "installable + first
evidence in 4 hours" acceptance criterion; the end-to-end walkthrough
lives in [`docs/getting-started/first-evidence.md`](getting-started/first-evidence.md).

```sh
# 1. Clone the repo (the bundle bind-mounts migrations/ + controls/ from it).
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas

# 2. Create your env file from the template and edit every CHANGE_ME value.
cp deploy/docker/.env.example deploy/docker/.env
${EDITOR:-vi} deploy/docker/.env
#    Generate strong values:  openssl rand -hex 32

# 3. Bring the whole stack up (builds images on first run).
just self-host-up
#    or, without `just`:
#    docker compose -f deploy/docker/docker-compose.yml \
#      --env-file deploy/docker/.env up -d --build

# 4. Watch the one-shot bootstrap finish (migrations + seed + SCF + controls).
just self-host-logs            # Ctrl-C once you see "bootstrap complete"

# 5. Confirm the platform is alive.
curl -fsS http://localhost:8080/health        # {"status":"ok","db":"ok"}

# 6. Open the UI and sign in with ATLAS_DEFAULT_USER_EMAIL / _PASSWORD.
open http://localhost:3000
```

Services in the bundle (`docker compose ps` / `just self-host-ps`):

| Service           | Role                                                                                                                  |
| ----------------- | --------------------------------------------------------------------------------------------------------------------- |
| `postgres`        | Postgres 16 — primary store (`pg-data` volume)                                                                        |
| `nats`            | NATS JetStream — evidence-ingest buffer (`nats-data` volume)                                                          |
| `minio`           | S3-compatible artifact store (`minio-data` volume)                                                                    |
| `minio-mc`        | one-shot — creates the artifacts bucket, then exits                                                                   |
| `atlas-migrate`   | **always-run** one-shot — applies pending migrations on every bring-up, idempotent + fail-closed, exits 0 (slice 473) |
| `atlas-bootstrap` | first-boot one-shot — seed + SCF import + control upload, exits                                                       |
| `atlas`           | platform server — gRPC `:50051`, HTTP `:8080` (`/health`)                                                             |
| `web`             | Next.js frontend — `:3000`                                                                                            |

`just` recipes for the bundle: `self-host-up`, `self-host-down`
(keeps data), `self-host-wipe` (`down -v` — deletes volumes),
`self-host-logs`, `self-host-ps`, `self-host-build`, `self-host-config`
(validates the compose file without starting anything).

### Manual smoke test

CI validates that `docker-compose.yml` parses (`just self-host-config`);
it does not run a full stack bring-up (the MinIO / NATS service-container
startup is too flaky for a fast CI gate). To smoke-test the bundle
yourself on a host with Docker:

```sh
cp deploy/docker/.env.example deploy/docker/.env   # edit the CHANGE_ME values
just self-host-up
# wait for the bootstrap one-shot to exit 0:
docker compose -f deploy/docker/docker-compose.yml ps atlas-bootstrap
curl -fsS http://localhost:8080/health             # expect HTTP 200
curl -fsS http://localhost:3000 -o /dev/null -w '%{http_code}\n'   # expect 200
just self-host-wipe                                # tear down + delete volumes
```

### Bootstrap credential — rotate it

`ATLAS_BOOTSTRAP_TOKEN` is a pre-shared admin token the one-shot
bootstrap container uses to upload the control bundles. It is a
convenience credential for first boot. Once you have signed in and
issued a real operator API key, **revoke or rotate the bootstrap
token** — it should not remain a long-lived admin credential.

---

## Quick start — the Watchtower auto-update example (server-only)

The example compose file at [`deploy/watchtower/docker-compose.example.yml`](../deploy/watchtower/docker-compose.example.yml)
is a slimmer, server-only deployment (no bundled frontend / NATS / MinIO)
focused on auto-update from GHCR:

```sh
# 1. Pick a deploy dir.
mkdir -p /opt/security-atlas && cd /opt/security-atlas

# 2. Pull the example compose file (or copy it from this repo).
curl -fsSL \
  https://raw.githubusercontent.com/mgoodric/security-atlas/main/deploy/watchtower/docker-compose.example.yml \
  -o docker-compose.yml

# 3. Edit the Postgres password and DATABASE_URL.
${EDITOR:-vi} docker-compose.yml

# 4. Bring it up.
docker compose up -d

# 5. Migrations apply automatically on every bring-up *of a compose file
#    that ships the always-run `atlas-migrate` one-shot* (slice 473+).
#    The platform binary does NOT expose a `migrate` subcommand. Migrations
#    are applied by the always-run `atlas-migrate` one-shot, which the
#    `atlas` backend is gated on (it will not serve until migrations have
#    completed). Watchtower only advances container *images* — it never
#    re-pulls the *compose file*, so a box still on a pre-473 compose has
#    no `atlas-migrate` service and silently drifts. See
#    [Upgrading: pull the compose, not just the image](#upgrading-pull-the-compose-not-just-the-image).
#    To apply migrations explicitly (e.g. ahead of bringing the server
#    up), run just the migrate step:
docker compose run --rm atlas-migrate

# 6. Confirm the platform is alive.
curl -fsSL http://localhost:8080/health
```

The example compose file brings up three containers:

- `security-atlas` — the platform server
- `security-atlas-postgres` — Postgres 16 (data persisted in the `atlas-pg-data` volume)
- `watchtower` — opt-in auto-updater (see below)

---

## Watchtower (opt-in auto-update from GHCR)

security-atlas publishes a new container image to `ghcr.io/mgoodric/security-atlas` on every release tag. Watchtower can pull and restart the platform automatically when a new tag lands.

**The pattern is label-based opt-in.** Only containers carrying the label `com.centurylinklabs.watchtower.enable=true` are touched. Postgres is deliberately NOT labelled — major upgrades require manual dump+restore.

```yaml
services:
  atlas:
    image: ghcr.io/mgoodric/security-atlas:latest
    labels:
      com.centurylinklabs.watchtower.enable: "true"

  watchtower:
    image: containrrr/watchtower:latest
    environment:
      WATCHTOWER_LABEL_ENABLE: "true"
      WATCHTOWER_POLL_INTERVAL: "86400" # poll daily
      WATCHTOWER_CLEANUP: "true" # remove old images after upgrade
      WATCHTOWER_ROLLING_RESTART: "true" # don't take everything down at once
```

### Verifying an auto-update worked

```sh
# Capture the image SHA before.
docker inspect security-atlas | jq -r '.[0].Image'

# Wait for a release tag and the next poll cycle (≤24h with the default).

# Compare.
docker inspect security-atlas | jq -r '.[0].Image'

# Different SHA = auto-update applied. Watchtower also logs each
# restart to its own container logs:
docker logs watchtower --tail=50
```

### What to pin

| Tag pattern      | Behavior                                               | Use when                                            |
| ---------------- | ------------------------------------------------------ | --------------------------------------------------- |
| `:latest`        | Every release auto-applies                             | You trust the project's release discipline          |
| `:0.3` (minor)   | Auto-updates within `0.3.x`, never auto-jumps to `0.4` | You want patches but explicit opt-in for new minors |
| `:0.3.5` (patch) | No auto-updates; Watchtower is effectively a no-op     | You want fully manual upgrade control               |

For production self-hosters, **pin to a minor** until you've done one or two upgrade cycles and built confidence in the release pipeline.

---

## Worked example — Unraid

[Unraid](https://unraid.net) is a popular self-host substrate for small teams and homelabs. The compose example above runs unchanged via the [Unraid Compose Manager plugin](https://forums.unraid.net/topic/114415-plugin-docker-compose-manager/).

```sh
# On the Unraid host:
ssh root@unraid.local

# Make a deploy dir on the array (not on /tmp).
mkdir -p /mnt/user/appdata/security-atlas
cd /mnt/user/appdata/security-atlas

# Pull the compose file.
curl -fsSL \
  https://raw.githubusercontent.com/mgoodric/security-atlas/main/deploy/watchtower/docker-compose.example.yml \
  -o docker-compose.yml

# Edit DATABASE_URL password.
nano docker-compose.yml

# Bring it up.
docker compose up -d
```

In the Unraid web UI:

1. **Settings → Compose Manager → Add Stack**
2. Point it at `/mnt/user/appdata/security-atlas`
3. Save — Unraid will surface the stack in the **Docker** tab and the containers' health/logs will live there alongside everything else on the host.

Watchtower polls hourly by default in Unraid examples; the 86400s (24h) interval in our compose file is more conservative and friendlier on cloud-provider rate limits if the host wakes up multiple containers at once.

---

## Database migrations across upgrades

**Migrations run automatically, on every bring-up, fail-closed (slice
473).** The bundle ships a dedicated `atlas-migrate` service that applies
any pending `migrations/sql/*.sql` not yet recorded in the
`schema_migrations` ledger — as the `atlas_migrate` role (`BYPASSRLS`, no
superuser), one transaction per file. It runs on **every** `docker
compose up`, not only first boot, and it is:

- **idempotent** — an already-current database applies nothing and exits
  0 with a `schema current — no migrations to apply` log line (no
  re-seed);
- **fail-closed** — a failing migration exits the migrate step non-zero
  with a `FATAL: migration '<filename>' failed` line, and the `atlas`
  backend (gated on `atlas-migrate` via
  `service_completed_successfully`) **does not start**. The platform
  never serves against a partially-migrated schema.

This is what makes a [Watchtower](#watchtower-opt-in-auto-update-from-ghcr)
auto-update safe: when Watchtower pulls a newer `atlas` image, the
`atlas-migrate` service advances in lockstep (it carries the same
auto-update label) and the backend waits for it to finish before serving.
A binary can never end up newer than its schema.

<!-- prettier-ignore-start -->
!!! note "Why this is a dedicated service (the 2026-06-05 incident)"
    Earlier bundles applied migrations only inside the **first-boot**
    `atlas-bootstrap` one-shot, which Watchtower never re-ran. An image
    update advanced the binary while the migrate step stayed pinned — the
    binary served against a stale schema and a downstream action failed
    with a masked HTTP 500. Splitting migrations into an always-run,
    backend-gating `atlas-migrate` service closes that gap. See the
    [Upgrade runbook](https://mgoodric.github.io/security-atlas/upgrade/).
<!-- prettier-ignore-end -->

### Upgrading: pull the compose, not just the image

**The rule:** upgrading the stack means pulling the updated compose file
**and** the `.env.example` deltas, then re-running `up -d` — not just
letting Watchtower pull new images.

```sh
git -C security-atlas pull            # or re-fetch the compose file
docker compose -f deploy/docker/docker-compose.yml pull
docker compose -f deploy/docker/docker-compose.yml up -d
```

**Why this matters.** Watchtower advances container **images**; it never
re-pulls the **compose file**. Services, labels, gating, volumes, and env
are operator-managed. A service added in a release — like slice 473's
`atlas-migrate` migrate-on-upgrade one-shot — only appears when you pull
the file. A box that has only ever let Watchtower update images is still
running its **first-boot compose**, where migrations ran once (via the
first-boot bootstrap) and never again. Every image bump that ships a new
migration then drifts the schema.

| What advances?         | Watchtower (image pull) | `git pull` + `up -d` |
| ---------------------- | :---------------------: | :------------------: |
| `atlas` binary         |           yes           |         yes          |
| new compose service    |           no            |         yes          |
| `atlas-migrate` gating |           no            |         yes          |
| new migrations applied | only if service present |         yes          |

#### Symptom of drift

An HTTP 500 shortly after an image bump — where the **backend log** shows
either:

- a CHECK-constraint violation, e.g. `violates check constraint
"..._action_check" (SQLSTATE 23514)` — a new allowed value the migration
  would have added is missing; or
- a missing relation or column, `SQLSTATE 42P01` (undefined table) /
  `42703` (undefined column) — the binary expects schema the migration
  would have created.

means the running binary is ahead of the schema: **migration drift** from
an image-only upgrade.

#### Recovery

**Preferred — adopt the slice-473 migrate service (one-time fix that also
prevents recurrence):**

```sh
git -C security-atlas pull            # refresh docker-compose.yml + .env.example
docker compose -f deploy/docker/docker-compose.yml pull
docker compose -f deploy/docker/docker-compose.yml up -d
```

The now-present `atlas-migrate` service applies every pending migration
before `atlas` serves, and stays in lockstep on future image bumps.

**Stop-gap — hand-apply pending migrations** (only if you cannot pull the
compose right now):

1. **Take a backup checkpoint FIRST.** Follow the
   [Backup and restore runbook](https://mgoodric.github.io/security-atlas/backup-restore/)
   (slice 432) — a `pg_dump` you have proven restores. Do not skip this:
   a hand-applied migration that goes wrong has no undo without it.
2. **Find the gap.** List `migrations/sql/*.sql` (ignore the `.down.sql`
   companions) and compare against the `schema_migrations` ledger
   (columns `filename`, `applied_at`); apply only the files not yet
   recorded, **in filename order** (the timestamp prefix is the order).
3. **Apply each pending migration and record it in the ledger** so the
   migrate service does not later double-apply it. Illustrative form
   (substitute your DB/role names; never paste real credentials into a
   shell history):

   ```sh
   { echo 'SET ROLE atlas_migrate;'; cat migrations/sql/<file>.sql; } \
     | psql -U postgres -d security_atlas -v ON_ERROR_STOP=1
   psql -U postgres -d security_atlas -c \
     "INSERT INTO schema_migrations (filename, applied_at) \
      VALUES ('<file>.sql', now()) ON CONFLICT DO NOTHING;"
   ```

4. Restart `atlas` and re-confirm `/health`. Then schedule the
   compose-pull above so the box is permanently on a slice-473 compose.

> The stop-gap is a recovery escape hatch, not the upgrade path. The
> supported, repeatable upgrade is always **pull the compose + `up -d`**.

For production, you can still apply migrations **explicitly before** the
new server takes traffic — run just the migrate step, which is the same
idempotent runner the stack uses automatically:

```sh
docker compose -f deploy/docker/docker-compose.yml pull
docker compose -f deploy/docker/docker-compose.yml stop atlas web
docker compose -f deploy/docker/docker-compose.yml run --rm atlas-migrate
docker compose -f deploy/docker/docker-compose.yml up -d atlas web
```

See the [Upgrade runbook](https://mgoodric.github.io/security-atlas/upgrade/)
for the version-pinning table, the verify checks, the rollback path, and
the major-Postgres-upgrade dump-and-restore.

---

## Backups

The full operator procedure — Postgres dump **and restore**, MinIO
`mc mirror` both directions, signing-key and bootstrap-token handling,
backup encryption at rest, and a **tested restore drill** — is the
canonical, published
**[Backup and restore runbook](https://mgoodric.github.io/security-atlas/backup-restore/)**.

The summary:

- **Postgres** — `pg_dump` to an encrypted, access-controlled,
  off-host S3-compatible store. The `pg-data` Docker volume is the
  on-disk state; the dump is the recoverable artifact.
- **Artifact store** — `mc mirror` to the offsite store, with versioning
  - lifecycle rules and server-side encryption at the bucket level.
- **Signing keys + secrets** — the OAuth keystore, `OSCAL_SIGNING_KEY`,
  and the bootstrap token are backed up **separately, with stricter
  access control** than the data dump. Rotate the bootstrap token after
  every restore.
- **Configuration** — keep `docker-compose.yml` + `.env` in a private,
  access-controlled repo.

Do not stop at "I have a dump." Run the
[restore drill](https://mgoodric.github.io/security-atlas/backup-restore/#tested-restore-drill)
to prove the dump restores and the restored evidence is intact.

### Automated backups + scheduled restore-verification

The manual procedure above is the belt-and-suspenders, full-fidelity path.
For day-to-day continuity, the `atlas` binary also runs an **in-process
automated backup** + a **scheduled restore-verification** — so your recovery
posture matches the
[BCP/DR plan](https://github.com/mgoodric/security-atlas/blob/main/docs/governance/business-continuity.md)
without depending on remembering to run the runbook. This operationalizes the
runbook above; it does not replace it.

What it does, each cycle (defaults: daily):

- **Backs up** the database as a logical SQL dump and writes it through a
  pluggable target — a local volume (default) or an S3-compatible bucket.
- **Rotates** old backups out (default: keep 7 daily + 4 weekly) so storage
  does not grow unbounded.
- **Verifies** the latest backup by restoring it into a throwaway ephemeral
  database, recomputing its sha256, running a smoke check, then destroying the
  ephemeral database. A backup you have never restored is not a backup.
- **Alerts** on failure: a failed backup or verification writes a durable
  status record and raises an in-app notification (delivered by the email
  channel when `ATLAS_SMTP_*` is configured), so a silently broken backup is
  loud, not discovered at recovery time.

It runs as a **deployment-privileged operation** — no tenant or user-facing
role can trigger or read a backup. The dump is a full cross-tenant copy; treat
the backup destination as crown-jewel-sensitive and encrypt it at rest (S3 SSE
or volume encryption) exactly as the runbook describes.

Configuration (all optional; sensible defaults for the single-VM bundle):

| Env var                        | Default                  | Meaning                                                          |
| ------------------------------ | ------------------------ | ---------------------------------------------------------------- |
| `ATLAS_BACKUP_TARGET`          | `local`                  | `local` (volume) or `s3`                                         |
| `ATLAS_BACKUP_DIR`             | `/var/lib/atlas/backups` | local-target directory (mount a volume here)                     |
| `ATLAS_BACKUP_S3_BUCKET`       | —                        | bucket for the `s3` target                                       |
| `ATLAS_BACKUP_S3_PREFIX`       | —                        | key prefix within the bucket                                     |
| `ATLAS_BACKUP_S3_ENDPOINT`     | —                        | S3-compatible endpoint (e.g. MinIO); uses standard `AWS_*` creds |
| `ATLAS_BACKUP_INTERVAL`        | `24h`                    | backup cadence                                                   |
| `ATLAS_BACKUP_VERIFY_INTERVAL` | `24h`                    | restore-verification cadence                                     |
| `ATLAS_BACKUP_KEEP_DAILY`      | `7`                      | dailies to retain                                                |
| `ATLAS_BACKUP_KEEP_WEEKLY`     | `4`                      | weeklies to retain                                               |
| `ATLAS_BACKUP_ALERT_RECIPIENT` | —                        | user id that receives failure notifications                      |

> **Scope note.** The automated dump is a logical (data + minimal-schema)
> dump tuned for continuity + restore-verification. For exact catalog fidelity
> (extensions, indexes, constraints, RLS policies) the manual `pg_dump` path in
> the runbook above remains the reference. Point-in-time recovery (WAL
> archiving) is out of scope for v1 — the logical-dump + off-host target is the
> v1 RPO mechanism.

---

## Evidence-staleness alerts (slice 439)

The platform watches evidence freshness and tells the operator when a control's
evidence crosses its freshness threshold — so you never have to keep a
side-spreadsheet of "what's about to expire." Two surfaces, both written to the
in-app notifications store and both delivered through any configured channel
(email / Slack / webhook):

| Surface           | What it is                                                         | Honest cadence (named, not "continuous")  |
| ----------------- | ------------------------------------------------------------------ | ----------------------------------------- |
| Per-control alert | One notification when a control's evidence becomes **stale**       | Staleness is **recomputed every 6 hours** |
| Weekly digest     | A summary of stale + approaching-stale controls with a top-10 list | Generated **every Monday at 09:00 UTC**   |

These are **scheduled, named-interval** signals — deliberately **not** framed
as real-time/continuous monitoring. The recompute interval is stated in every
alert; the digest names the week it covers and the Monday-09:00-UTC cadence.

**Tuning the cadence.**

| Variable                   | Default | Meaning                                                             |
| -------------------------- | ------- | ------------------------------------------------------------------- |
| `ATLAS_STALENESS_INTERVAL` | `6h`    | How often the rollup recomputes staleness (e.g. `1h` for dev loops) |

The weekly digest fires on the Monday-09:00-UTC tick regardless of the recompute
interval; both writes are idempotent, so an extra tick never double-delivers.
The "approaching-stale" early-warning band is **14 days** before a control's
`valid_until` horizon.

**Opting out.** Each user is opted **in** by default. To stop receiving the
in-app staleness surface, set the per-kind preference for the
`evidence_staleness` event on the `in_app` channel to off
(`PATCH /v1/me/preferences`, or the notification preferences UI). The opt-out is
honored by the producer — an opted-out user receives no staleness alert or
digest. Per-control custom thresholds and a dedicated preferences page are
follow-on work; v1 reuses the per-control freshness class as the threshold.

**Recipients.** All active users of a tenant receive the tenant's staleness
notifications (for the v1 solo-operator deployment, that is the operator).

---

## Monitoring

The platform exports OTEL traces, metrics, and logs by default. Point `OTEL_EXPORTER_OTLP_ENDPOINT` at your collector of choice. The bundled docker-compose at [`deploy/docker/observability-compose.yml`](../deploy/docker/observability-compose.yml) brings up Prometheus + Grafana + Tempo + Loki for evaluation.

Once telemetry is enabled, see [`docs/operator/observability-tuning.md`](operator/observability-tuning.md) for keeping trace-emission overhead bounded under load — in particular the `OTEL_TRACES_SAMPLER` recipe for high database query rates.

---

## Upgrading from an older release

See [`CHANGELOG.md`](../CHANGELOG.md) for breaking-change notes per release. When a release notes a breaking change, the upgrade path is documented in that release's notes.

---

## Getting help

- Open a [GitHub issue](https://github.com/mgoodric/security-atlas/issues) for bugs or feature requests
- Read [`CLAUDE.md`](../CLAUDE.md) for the project's constitutional principles before proposing architecture-shaping changes
- Security issues — see [`SECURITY.md`](../SECURITY.md), **not** the public issue tracker
