# Install — self-host quickstart

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - Prerequisites for a single-VM self-host
    - How to bring the platform up with one `just` command
    - How to confirm it is healthy and sign in
<!-- prettier-ignore-end -->

The `deploy/docker/docker-compose.yml` bundle is the **whole platform on
one host**: Postgres 16, NATS JetStream, MinIO, the `atlas` server, and
the Next.js frontend. On first boot a one-shot `atlas-bootstrap`
container applies migrations, seeds the default tenant + scope cell +
local user, imports the SCF catalog, and uploads the 50 SOC 2 control
bundles.

## Prerequisites

| Requirement             | Version                                                  |
| ----------------------- | -------------------------------------------------------- |
| Docker Engine           | 24+                                                      |
| `docker compose` plugin | included with current Docker Engine                      |
| `git`                   | any current version                                      |
| VM size                 | 4 vCPU / 16 GB RAM is comfortable for the bundled stack  |
| Disk                    | 20 GB available — Postgres + MinIO live in named volumes |

For phase 3 of [First audit](first-audit.md) you will also want an AWS
account where you can create a read-only IAM role; not required to bring
the bundle up.

## Step 1 — clone and configure

```sh
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas

cp deploy/docker/.env.example deploy/docker/.env
${EDITOR:-vi} deploy/docker/.env
```

At minimum, set strong values for every `CHANGE_ME` in `.env`:

| Variable                      | How to generate                                              |
| ----------------------------- | ------------------------------------------------------------ |
| `POSTGRES_PASSWORD`           | `openssl rand -hex 24`                                       |
| `ATLAS_APP_PASSWORD`          | `openssl rand -hex 24`                                       |
| `MINIO_ROOT_PASSWORD`         | `openssl rand -hex 24`                                       |
| `BEARER_HASH_KEY`             | `openssl rand -hex 32` (32-byte key for token hashes)        |
| `ATLAS_BOOTSTRAP_TOKEN`       | `openssl rand -hex 32` (legacy fixed-token admin credential) |
| `ATLAS_DEFAULT_USER_EMAIL`    | the email you will sign in with                              |
| `ATLAS_DEFAULT_USER_PASSWORD` | the password you will sign in with                           |

## Step 2 — bring it up

```sh
just self-host-up      # builds images on first run, then `up -d`
just self-host-logs    # follow boot logs; Ctrl-C after "bootstrap complete"
```

First boot pulls Postgres / NATS / MinIO and builds three images — budget
~5 minutes on a mid-size VM, longer on a cold Docker cache. The
`atlas-bootstrap` container exits 0 when seeding is done; the `atlas` and
`web` services start once it has.

## Step 3 — confirm it is healthy

```sh
curl -fsS http://localhost:8080/health
# {"status":"ok","db":"ok"}
```

If `/health` is not 200 after a few minutes, the failure is almost always
visible in the bootstrap logs:

```sh
docker compose -f deploy/docker/docker-compose.yml logs atlas-bootstrap
docker compose -f deploy/docker/docker-compose.yml logs atlas
```

## Step 4 — sign in

Open <http://localhost:3000> and sign in with the
`ATLAS_DEFAULT_USER_EMAIL` / `ATLAS_DEFAULT_USER_PASSWORD` from your
`.env`. This is **local mode** — no external IdP required for first
sign-in. OIDC is a later configuration step, not a prerequisite.

<!-- prettier-ignore-start -->
!!! info "First-time sign-in"

    The `/login` page detects fresh-install state and shows three
    orthogonal ways to find the bootstrap admin token (container logs,
    Helm logs, and the file at `${ATLAS_DATA_DIR}/bootstrap-token`,
    mode 0600). The file is atomically deleted on first successful
    sign-in. If you get stuck, see
    [Troubleshooting → First-time login](troubleshooting/first-login.md).
<!-- prettier-ignore-end -->

## Bootstrap credentials — what gets created

First boot creates **two distinct credentials** the operator should know
about. They live at different lifecycle layers and are managed
separately.

| Credential                       | Purpose                                                                                  | Where it lives                                                                                                                                                    |
| -------------------------------- | ---------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Default user (local sign-in)** | The human account you sign in with at the web UI on first boot                           | `ATLAS_DEFAULT_USER_EMAIL` + `ATLAS_DEFAULT_USER_PASSWORD` in your `.env`                                                                                         |
| **Bootstrap OAuth client**       | Machine identity the one-shot `atlas-bootstrap` container uses to upload control bundles | Issued at runtime by `atlas-bootstrap`; persisted to `/var/lib/atlas-bootstrap/oauth-bootstrap-credentials.json` (mode 0600) on the `atlas-bootstrap-data` volume |

The bootstrap OAuth client is created automatically — you do not
configure it. It is named `atlas-bootstrap-controls-<8-hex-tenant>` and
its `client_secret` never leaves the `atlas-bootstrap-data` volume
inside the docker network. To inspect it:

```sh
docker compose -f deploy/docker/docker-compose.yml run --rm \
  --entrypoint sh atlas-bootstrap \
  -c 'cat /var/lib/atlas-bootstrap/oauth-bootstrap-credentials.json'
```

Re-running `docker compose up` reuses the existing client (idempotent).
A `docker compose down -v` wipes the volume; the next bring-up issues a
new client.

<!-- prettier-ignore-start -->
!!! info "Legacy fixed-token credential"

    The `ATLAS_BOOTSTRAP_TOKEN` from your `.env` is the **operator
    sign-in convenience credential** (slice 037). The `atlas` service
    still mints it as a one-shot fixed-token admin credential so the
    `/login` page's three orthogonal first-time-sign-in paths
    continue to work. It is independent of the bootstrap OAuth client
    above; you rotate them separately. A future release will retire
    the fixed-token credential entirely.
<!-- prettier-ignore-end -->

## What's seeded for you

- A default tenant. (security-atlas is multi-tenant from day one; the UI
  hides tenant chrome when only one tenant exists.)
- A default scope cell — `environment = prod`, the "everything" cell a
  solo operator starts from. Add scope dimensions as your environment
  grows.
- The full SCF catalog — the spine every framework maps through.
- 50 SOC 2 control bundles — each is a control definition plus its
  `evidence_query`. Forkable as-is.

## Verifying your install

The build version, commit, and build time are baked into the binary at
release time and surface in three places. All three report the same
value (single source of truth: Go ldflags).

```sh
# JSON, scriptable
curl -s http://localhost:8080/v1/version

# Human-readable banner (inside the running container)
docker compose -f deploy/docker/docker-compose.yml exec atlas /usr/local/bin/atlas --version

# OCI image annotation
docker inspect deploy-atlas:latest \
  --format '{{ index .Config.Labels "org.opencontainers.image.version" }}'
```

The same version also renders in the bottom-right of every page in the
web UI — click the trigger to expand a small panel with `commit`,
`build_time`, and `go_version`. No phone-home call; the value is read
once at app boot and cached for the session.

## Tear down

```sh
just self-host-down    # stop everything, KEEP the data volumes
just self-host-wipe    # stop everything AND delete the volumes (full reset)
```

## Production guidance

For multi-host, backups, monitoring, upgrades, and the Watchtower
auto-update pattern, see
[`docs/SELF_HOSTING.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md)
in the repo.

## Next steps

- [Framework setup →](framework-setup.md) — load the SCF catalog and the
  SOC 2 crosswalk

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
