# Edge deploy channel — operator runbook

**Status:** introduced in slice 207. Pre-requisite reading:
[`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) for the stable channel
setup.

## TL;DR

The **edge channel** is a second security-atlas deployment that runs
in parallel with the stable channel and auto-updates within ~5–10 min
of every merge to `main`. It exists so the maintainer (and anyone
running a homelab instance) can validate end-to-end on a real
deployment without waiting for a release-please cycle.

| Channel               | Hostname                    | Auto-update     | Image tag                | Cadence             |
| --------------------- | --------------------------- | --------------- | ------------------------ | ------------------- |
| Stable                | `atlas.home.gmoney.sh`      | Manual          | `vX.Y.Z`                 | every 2–7 days      |
| **Edge (this guide)** | `atlas-edge.home.gmoney.sh` | Watchtower 5min | `:edge` + `:main-<sha7>` | every merge to main |

The edge instance must use a **separate Postgres database** + a
**separate MinIO bucket** + a **separate keystore path** — a
schema-breaking migration on `main` runs against edge BEFORE it
reaches stable, and we never want it to corrupt stable.

## What edge is for

- Validating UI changes against a deployed instance before they cut
  to stable.
- Closing the iteration loop on bug fixes that only reproduce in a
  production-build deployment (slice 206 was the load-bearing example
  — the BFF cookie behaviour differed between `npm run dev` and the
  shipped Docker image).
- Running [slice 204's UI parity audit fleet](../../docs/issues/204-comprehensive-page-by-page-ui-parity-audit.md)
  against a real deployment that matches `main`.

## What edge is NOT for

- **Customer-facing demos.** Edge can crashloop on a bad commit
  (that's its job — find the bad commit before release). Use the
  stable channel for any demo or auditor walkthrough.
- **Sharing data with stable.** Edge runs its own Postgres + MinIO +
  keystore. Tenant IDs in edge are NOT portable to stable.
- **Public internet exposure.** Edge is operator-only by default (see
  Access control below). Public exposure would publicize half-baked
  features without the review gates that release-please enforces.

## One-time setup on Unraid

The maintainer's reference deployment runs on Unraid; the same
docker-compose template works on any Linux host with Docker 24+.

### 1. Clone the repo (or pull on update)

```sh
git clone https://github.com/mgoodric/security-atlas.git ~/security-atlas
# Or, on an existing checkout:
cd ~/security-atlas && git pull
```

You only need the repo for the compose template + `.env.example` —
the actual binaries come from GHCR. A `git pull` once per docs-update
is enough; Watchtower handles image updates.

### 2. Create the edge .env file

```sh
cd ~/security-atlas
cp deploy/docker/.env.example deploy/docker/.env.edge
${EDITOR:-vi} deploy/docker/.env.edge
```

Generate fresh random values for every credential (`openssl rand
-hex 32`). Do NOT reuse values from your stable `.env` — running two
instances with the same `BEARER_HASH_KEY` or `ATLAS_BOOTSTRAP_TOKEN`
defeats the isolation guarantee.

Extra env vars the edge compose adds beyond the stable `.env`:

| Variable                    | Purpose                           | Required?       |
| --------------------------- | --------------------------------- | --------------- |
| `DATABASE_URL_APP_EDGE`     | atlas_app DSN against the edge DB | required        |
| `DATABASE_URL_MIGRATE_EDGE` | atlas_migrate DSN against edge DB | required        |
| `EDGE_POSTGRES_PORT`        | host port for edge Postgres       | default `5532`  |
| `EDGE_NATS_PORT`            | host port for edge NATS           | default `4322`  |
| `EDGE_NATS_MONITOR_PORT`    | host port for edge NATS monitor   | default `8322`  |
| `EDGE_MINIO_PORT`           | host port for edge MinIO API      | default `9100`  |
| `EDGE_MINIO_CONSOLE_PORT`   | host port for edge MinIO console  | default `9101`  |
| `EDGE_ATLAS_HTTP_PORT`      | host port for edge atlas HTTP     | default `8180`  |
| `EDGE_ATLAS_GRPC_PORT`      | host port for edge atlas gRPC     | default `50151` |
| `EDGE_WEB_PORT`             | host port for edge web frontend   | default `3100`  |

The DSNs point at the edge Postgres container's INTERNAL hostname
(`postgres-edge`), not the stable one:

```dotenv
DATABASE_URL_APP_EDGE=postgres://atlas_app:${ATLAS_APP_PASSWORD}@postgres-edge:5432/security_atlas?sslmode=disable
DATABASE_URL_MIGRATE_EDGE=postgres://atlas_migrate@postgres-edge:5432/security_atlas?sslmode=disable
```

### 3. Bring the edge stack up

```sh
docker compose \
  -f deploy/docker/docker-compose.edge.yml \
  --env-file deploy/docker/.env.edge \
  -p security-atlas-edge \
  up -d
```

The `-p security-atlas-edge` puts the edge containers in a separate
compose project namespace. Without it, edge and stable would
interleave under the same project key and `docker compose down` on
one would tear down the other.

Confirm both stacks are healthy:

```sh
docker compose -p security-atlas      ps  # stable
docker compose -p security-atlas-edge ps  # edge
```

### 4. DNS + reverse proxy (NPM)

Add a DNS A record for `atlas-edge.home.gmoney.sh` pointing at the
Unraid box (the same IP as `atlas.home.gmoney.sh`; the reverse proxy
splits by hostname).

In Nginx Proxy Manager (or your reverse proxy of choice), add a
proxy host:

| Field                 | Value                                                  |
| --------------------- | ------------------------------------------------------ |
| Domain Names          | `atlas-edge.home.gmoney.sh`                            |
| Scheme                | `http`                                                 |
| Forward Hostname/IP   | the Docker host's LAN IP (the box running the compose) |
| Forward Port          | `3100` (or whatever `EDGE_WEB_PORT` you set)           |
| Cache Assets          | OFF                                                    |
| Block Common Exploits | ON                                                     |
| Websockets Support    | ON                                                     |
| Force SSL             | ON (Let's Encrypt or your own cert)                    |
| HTTP/2 Support        | ON                                                     |

The `/v1` API path that the browser hits will land at `web-edge`
which proxies it through the BFF to `atlas-edge` on the internal
Docker network — same shape as stable.

### 5. Watchtower

The edge stack does NOT bundle Watchtower itself; the operator runs
Watchtower once for the host and configures it with label-based
discovery so ONLY the edge atlas + web containers auto-update:

```sh
docker run -d \
  --name watchtower \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  containrrr/watchtower:latest \
    --label-enable \
    --cleanup \
    --interval 300
```

Flag breakdown:

| Flag             | Effect                                                                                                                                                                 |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--label-enable` | Only watch containers that have `com.centurylinklabs.watchtower.enable=true`. The `atlas-edge` + `web-edge` services have this label set in `docker-compose.edge.yml`. |
| `--cleanup`      | Delete the old image after a successful update so the GHCR pull doesn't accumulate disk space.                                                                         |
| `--interval 300` | Poll every 300 seconds (5 min). Slice 207 D4 — operator can tune.                                                                                                      |

**Important:** the stable `atlas` and `web` containers in
`docker-compose.yml` do NOT have the watchtower-enable label, so
Watchtower leaves them alone. Stable updates happen only when the
operator manually runs `docker compose pull` after a release-please
PR merges.

## How to confirm what's running on edge

Open `https://atlas-edge.home.gmoney.sh` and look at the footer of
any page — the `VersionFooter` component (slice 072) renders
`edge · <short-commit>`. Click it to reveal the full build_time +
git commit + Go version.

Cross-check from the host:

```sh
docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' \
  $(docker compose -p security-atlas-edge ps -q atlas-edge)
```

That label is set by `container-publish.yml` to the full SHA of the
commit the image was built from.

## How to roll back

If a bad commit lands on `main`, Watchtower will pull it within ~5
min. Roll back by pinning the edge atlas to the previous
`:main-<sha7>` tag:

```sh
docker pull ghcr.io/mgoodric/security-atlas:main-<prev-sha7>
docker pull ghcr.io/mgoodric/security-atlas-web:main-<prev-sha7>

# Edit docker-compose.edge.yml temporarily OR set an env override:
EDGE_ATLAS_TAG=main-<prev-sha7>  \
  docker compose -p security-atlas-edge \
  -f deploy/docker/docker-compose.edge.yml up -d
```

(If you want the `EDGE_ATLAS_TAG` mechanism, extend the compose
file's `image:` line to `image: ghcr.io/.../security-atlas:${EDGE_ATLAS_TAG:-edge}`
— that's a follow-on slice. Today, edit the compose file inline and
revert when the fix lands.)

Once you're pinned on a working tag, Watchtower will STILL try to
pull `:edge` because it's the floating tag. Either stop Watchtower
or rename the container's label to `com.centurylinklabs.watchtower.enable=false`
until you're ready to resume auto-updates:

```sh
docker label add \
  $(docker compose -p security-atlas-edge ps -q atlas-edge) \
  com.centurylinklabs.watchtower.enable=false
```

## How to wipe + recreate edge

The edge database is intentionally disposable. To start fresh
(typically because a migration broke and forward-fix is harder than
just resetting):

```sh
docker compose -p security-atlas-edge \
  -f deploy/docker/docker-compose.edge.yml \
  down -v   # the -v removes the edge volumes
docker compose -p security-atlas-edge \
  -f deploy/docker/docker-compose.edge.yml \
  --env-file deploy/docker/.env.edge \
  up -d
```

The first bring-up after a wipe re-runs `atlas-bootstrap-edge` which
re-applies all migrations + re-imports the SCF catalog + re-uploads
control bundles. This takes ~2–3 min on a 4 vCPU box.

## Migration safety

A schema-breaking commit on `main` will run its migrations against
the edge Postgres on the next Watchtower pull. **This is by design**
— edge is the canary for migration breakage. The workflow is:

1. Merge a feature branch to `main`.
2. Edge auto-updates within ~5–10 min.
3. The operator opens `atlas-edge.home.gmoney.sh`. If the UI loads
   and the smoke tests pass, the migration succeeded and the change
   is safe for the next release-please cut.
4. If migrations crash or the UI errors, either:
   - **Forward fix:** push another commit on `main` that fixes the
     migration. Edge picks it up in another ~5 min.
   - **Revert:** open a revert PR; once merged, edge picks up the
     revert and stable never sees the bad commit.

Edge is the cheap test channel; stable is the production channel.
Never promote a commit to stable until edge has confirmed it green.

## Access control on `atlas-edge.home.gmoney.sh`

Edge is operator-only by default. **Public exposure is not supported.**
Slice 207 D6 — pick one of:

### Option A — Tailscale (recommended for solo operators)

If you're on Tailscale, bind the NPM proxy host to the tailnet
interface only (`100.x.x.x` rather than `0.0.0.0`). Anyone not on
your tailnet can't reach the hostname.

### Option B — Cloudflare Access

If you front the Unraid box with Cloudflare, add a Cloudflare Access
policy on `atlas-edge.home.gmoney.sh` requiring a specific email
(yours) to be authenticated via Cloudflare Zero Trust. The browser
hits Cloudflare first; only authenticated requests reach NPM.

### Option C — NPM IP allowlist

In the NPM proxy host's "Access List" tab, create an allowlist with
your known LAN/VPN IP ranges and attach it to the
`atlas-edge.home.gmoney.sh` host. Simpler than tailnet but requires
manual IP updates when you change networks.

### Option D — HTTP Basic auth (NOT recommended)

NPM's built-in basic auth conflicts with the atlas session cookie —
the basic-auth prompt fires on every request including the BFF
`/api/*` routes, which the browser fetches in the background. Pick
A/B/C instead.

## Troubleshooting

### "Edge keeps pulling but nothing changes"

Watchtower only updates containers when the upstream image digest
changes. If you push two commits to `main` and the diff doesn't
change the binary's content (e.g. docs-only change), the build will
produce the same digest and Watchtower will skip the update.

Verify via the GHCR UI that a new `:main-<sha7>` tag exists for your
commit. If yes, check Watchtower's logs:

```sh
docker logs watchtower --tail=50
```

### "Edge crashed; how do I get the logs?"

```sh
docker compose -p security-atlas-edge logs --tail=200 atlas-edge
docker compose -p security-atlas-edge logs --tail=200 atlas-bootstrap-edge
docker compose -p security-atlas-edge logs --tail=200 web-edge
```

For migration crashes, atlas-bootstrap-edge is usually the first
container to fail; check its logs first.

### "I want both edge and stable to share a Postgres cluster"

**Do not do this.** P0-A4 of slice 207 forbids it. A schema-breaking
migration on edge would either fail (because stable already ran the
old schema) or succeed and corrupt stable's view of the data. The
docker-compose.edge.yml file enforces separation by giving the edge
Postgres a distinct volume + a distinct in-network hostname.

## See also

- [`Plans/canvas/10-roadmap.md`](../../Plans/canvas/10-roadmap.md) —
  the broader continuous-deploy roadmap context.
- [`.github/workflows/container-publish.yml`](../../.github/workflows/container-publish.yml)
  — the workflow that builds + pushes edge images.
- [`scripts/edge-image-cleanup.sh`](../../scripts/edge-image-cleanup.sh)
  — the GHCR retention pruner (D2 hybrid policy).
- [`docs/audit-log/207-edge-deploy-channel-decisions.md`](../audit-log/207-edge-deploy-channel-decisions.md)
  — the design decisions log for this slice.
