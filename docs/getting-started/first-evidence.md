# Getting started: from clone to first evidence in 4 hours

This is the end-to-end walkthrough behind security-atlas's binary v1
acceptance criterion: **a solo security leader can install the platform,
have it seeded, and produce a first real evidence record within four
hours — without reaching for Vanta or a spreadsheet to fill a gap.**

Realistically the hands-on time is well under an hour; the four-hour
budget is the generous outer bound that includes reading, generating
credentials, and setting up an AWS IAM role. The steps below are the
critical path.

| Phase                                    | Wall-clock budget |
| ---------------------------------------- | ----------------- |
| 1. Bring the bundle up                   | ~10 min           |
| 2. Sign in + look around the seeded data | ~10 min           |
| 3. Wire the AWS connector                | ~30–60 min        |
| 4. Produce the first evidence record     | ~5 min            |

Prerequisites: a host with Docker Engine 24+ and the `docker compose`
plugin, `git`, and (for phase 3) an AWS account where you can create a
read-only IAM role.

---

## Phase 1 — bring the bundle up

The `deploy/docker/docker-compose.yml` bundle is the whole platform on one
host: Postgres 16, NATS JetStream, MinIO, the `atlas` server, and the
Next.js frontend. On first boot a one-shot `atlas-bootstrap` container
applies the migrations, seeds a default tenant + scope + local user,
imports the SCF catalog, and uploads the 50 SOC 2 control bundles.

```sh
# Clone the repo — the bundle bind-mounts migrations/ and controls/ from it.
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas

# Create your environment file and edit every CHANGE_ME value.
cp deploy/docker/.env.example deploy/docker/.env
${EDITOR:-vi} deploy/docker/.env
```

In `deploy/docker/.env`, at minimum set strong values for:

- `POSTGRES_PASSWORD`, `ATLAS_APP_PASSWORD`
- `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD`
- `BEARER_HASH_KEY` — `openssl rand -hex 32`
- `ATLAS_BOOTSTRAP_TOKEN` — `openssl rand -hex 32`
- `ATLAS_DEFAULT_USER_EMAIL`, `ATLAS_DEFAULT_USER_PASSWORD` — this is the
  account you sign in with.

Then bring it up:

```sh
just self-host-up          # builds images on first run, then `up -d`
just self-host-logs        # follow the boot; Ctrl-C after "bootstrap complete"
```

First boot builds three images and pulls Postgres / NATS / MinIO — budget
~5 minutes on a mid-size VM (4 vCPU / 16 GB RAM), more on a cold Docker
cache. The `atlas-bootstrap` container exits 0 when seeding is done; the
`atlas` and `web` services start once it has.

Confirm the platform is alive:

```sh
curl -fsS http://localhost:8080/health
# {"status":"ok","db":"ok"}
```

If `/health` is not 200 after a few minutes, check the bootstrap logs:

```sh
docker compose -f deploy/docker/docker-compose.yml logs atlas-bootstrap
docker compose -f deploy/docker/docker-compose.yml logs atlas
```

---

## Phase 2 — sign in and look at the seeded data

Open the UI at <http://localhost:3000> and sign in with the
`ATLAS_DEFAULT_USER_EMAIL` / `ATLAS_DEFAULT_USER_PASSWORD` from your
`.env`. This is **local mode** — no external identity provider is
required for first sign-in. (You can wire OIDC later; it is an opt-in
configuration step, not a prerequisite.)

What the bootstrap already seeded for you:

- **A default tenant.** The solo operator is a single-tenant deployment
  of the multi-tenant platform — the UI hides tenant chrome when there is
  only one.
- **A default scope cell** — `environment = prod`, the "everything" cell
  a solo operator starts from. Add more scope dimensions later as your
  environment grows.
- **The SCF catalog** — the Secure Controls Framework anchors, the
  canonical control spine every framework maps through.
- **The 50 SOC 2 control bundles** — visible in the catalog. Each is a
  control-as-code bundle: a control definition plus its evidence query.

Browse the catalog and open a few controls. Note that controls like
`soc2_cc6_1_logical_access_baseline` declare an `evidence_query` against a
specific `evidence_kind` — that is the contract a connector or pusher
fulfils.

---

## Phase 3 — wire the AWS connector

The first connector is AWS. In v1 it collects one evidence kind:
`aws.s3.bucket_encryption_state.v1` — the default-encryption posture of
every S3 bucket in an account. It authenticates by **assuming a
read-only IAM role** (no static access keys).

### 3a. Create a read-only IAM role in AWS

In the AWS account you want to assess, create an IAM role the connector
can assume. It needs only read access to S3 bucket metadata —
`s3:GetEncryptionConfiguration` and `s3:ListAllMyBuckets`. Note the role
ARN.

### 3b. Issue a bearer token for the connector

The connector pushes evidence to the platform with a bearer token. From a
host that can reach the atlas API:

```sh
# Build the CLI once (or use the atlas-cli container image).
go build -o /tmp/atlas-cli ./cmd/atlas-cli

# Issue a credential for the default tenant.
/tmp/atlas-cli credentials issue \
  --endpoint http://localhost:8080 \
  --token "$ATLAS_BOOTSTRAP_TOKEN" \
  --tenant 00000000-0000-4000-8000-000000000001
```

The command prints a new bearer token once — copy it.

> The `ATLAS_BOOTSTRAP_TOKEN` you set in `.env` is a first-boot
> convenience admin credential. Once you have issued your real
> credentials, **rotate or revoke the bootstrap token**.

### 3c. Run the connector

```sh
just connector-build       # builds ./bin/aws-connector

SECURITY_ATLAS_ENDPOINT=localhost:50051 \
SECURITY_ATLAS_TOKEN=<the token from 3b> \
AWS_ROLE_ARN=<your read-only role ARN> \
AWS_REGION=us-east-1 \
AWS_ENVIRONMENT=prod \
just connector-run aws aws.s3.bucket_encryption_state.v1
```

---

## Phase 4 — confirm the first evidence record

The connector run pushes one evidence record per S3 bucket into the
append-only evidence ledger. Confirm it landed:

- In the UI, open the **Evidence** view — you should see fresh
  `aws.s3.bucket_encryption_state.v1` records, one per bucket, stamped
  with an `observed_at` timestamp and a sha256 content hash.
- The `soc2_cc6_*` controls whose evidence query reads that kind now have
  real evidence backing them instead of a gap.

That is the first-evidence milestone: a fresh clone, seeded with the SCF
spine and 50 SOC 2 controls, now producing real, hashed, point-in-time
evidence from a live cloud account — all from one `docker compose` bundle.

---

## Tear down

```sh
just self-host-down        # stop everything, KEEP the data volumes
just self-host-wipe        # stop everything AND delete the volumes (full reset)
```

---

## Where to go next

- [`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) — production self-host
  guidance: backups, monitoring, upgrades, Watchtower auto-update.
- [`CLAUDE.md`](../../CLAUDE.md) — the platform's constitutional
  principles. Read this before proposing architecture-shaping changes.
- Add more connectors, define your scope dimensions, and start mapping
  your real environment — the SCF spine means one control satisfies many
  frameworks at once.
