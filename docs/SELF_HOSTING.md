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

| Service           | Role                                                              |
| ----------------- | ----------------------------------------------------------------- |
| `postgres`        | Postgres 16 — primary store (`pg-data` volume)                    |
| `nats`            | NATS JetStream — evidence-ingest buffer (`nats-data` volume)      |
| `minio`           | S3-compatible artifact store (`minio-data` volume)                |
| `minio-mc`        | one-shot — creates the artifacts bucket, then exits               |
| `atlas-bootstrap` | one-shot — migrations + seed + SCF import + control upload, exits |
| `atlas`           | platform server — gRPC `:50051`, HTTP `:8080` (`/health`)         |
| `web`             | Next.js frontend — `:3000`                                        |

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

# 5. Apply migrations once.
docker compose exec atlas atlas migrate up

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

`atlas migrate up` is idempotent — running it against an already-current database is a no-op. Watchtower restarts the container but does not run migrations. The platform itself runs migrations on startup if `ATLAS_MIGRATE_ON_START=true` is set; for production we recommend leaving that **off** and running migrations manually so a bad migration cannot brick an auto-update:

```sh
# Before applying a new release manually:
docker compose pull atlas
docker compose run --rm atlas atlas migrate up
docker compose up -d atlas
```

If you have Watchtower turned on, set `ATLAS_MIGRATE_ON_START=true` only if every release ships with reversible migrations and you've established that the project's release discipline catches forward-only migrations during review.

---

## Backups

- **Postgres** — `pg_dump` nightly to your S3-compatible store. The `atlas-pg-data` Docker volume is the on-disk state.
- **Artifact store** — versioning + lifecycle rules at the bucket level. The platform's evidence ledger has a sha256 per record; a corrupted artifact is detectable by re-running `atlas evidence verify`.
- **Configuration** — keep `docker-compose.yml` + `.env` (if you use one) in a private repo.

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
