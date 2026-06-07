# Backup and restore

This runbook is the hands-on operator procedure for backing up a
self-hosted security-atlas deployment and — the load-bearing half —
**restoring it and proving the restore is faithful**. A backup you
cannot restore is not a backup; a restore you cannot verify is not a
recovery.

It operationalizes the project's
[Business continuity & disaster recovery plan](https://github.com/mgoodric/security-atlas/blob/main/docs/governance/business-continuity.md)
(the BCP/DR plan). The BCP/DR plan answers _what the project commits to_
(RTO/RPO tiers, restore scenarios for the maintainer-operated instance);
this runbook gives an operator the _runnable commands_ for their own
deployment. Where the two overlap, this page links the plan rather than
restating it.

<!-- prettier-ignore-start -->
!!! warning "Backups are the crown jewels"
    A backup contains everything sensitive in the system: tenant
    evidence (confidential customer data) and signing keys. An
    unencrypted dump sitting in a bucket is a breach waiting to happen.
    Encryption at rest and access control on the backup destination are
    not optional — they are covered in
    [Encrypt the backup](#encrypt-the-backup-at-rest) and
    [Signing keys and secrets](#signing-keys-and-secrets) below.
<!-- prettier-ignore-end -->

---

## What you are backing up

The [self-host bundle](install.md) brings up six stateful surfaces.
Three hold data you must back up; the rest are reconstructed from them.

| Surface                       | What it holds                                                                                            | Volume                                 | Back up?                                |
| ----------------------------- | -------------------------------------------------------------------------------------------------------- | -------------------------------------- | --------------------------------------- |
| **Postgres**                  | The primary store: control graph, evidence ledger, risk register, RLS policy set, audit log, tenant rows | `pg-data`                              | **Yes — the system of record**          |
| **Object store (MinIO / S3)** | Evidence artifacts larger than the inline threshold                                                      | `minio-data` (MinIO) or your S3 bucket | **Yes — referenced by the ledger**      |
| **Signing keys + secrets**    | OAuth JWT signing keystore, `OSCAL_SIGNING_KEY`, the bootstrap token, `BEARER_HASH_KEY`                  | `atlas-data` + your `.env`             | **Yes — separately, stricter handling** |
| NATS JetStream                | Evidence-ingest buffer (in-flight, not durable record)                                                   | `nats-data`                            | No — drains into Postgres               |
| `atlas` / `web` containers    | Stateless application code                                                                               | none                                   | No — rebuilt from the image             |

The **evidence ledger in Postgres carries a sha256 content hash per
record** (the Evidence SDK push contract computes the canonical-JSON
sha256 at ingest). That property is the substrate the restore drill
leans on: a restored ledger whose hashes still resolve is a faithful
restore. See canvas invariant #3 (append-only ledger) — this is why
full object-store loss is recoverable at all (BCP/DR plan Scenario C).

<!-- prettier-ignore-start -->
!!! note "Placeholders"
    Every host, bucket, key, and credential below is a **placeholder**.
    Replace `OFFSITE_BUCKET`, `backup.example.internal`,
    `s3.example.com`, `CHANGE_ME`, and similar with your own values.
<!-- prettier-ignore-end -->

---

## Backup destination — before you dump anything

Pick a destination _off the host that runs the platform_. A backup on
the same disk as the data does not survive a disk failure. The
destination must be:

- **Encrypted at rest.** Either encrypt the artifact before it leaves
  the host (see [Encrypt the backup](#encrypt-the-backup-at-rest)) or
  use server-side encryption (SSE) on the destination bucket — ideally
  both.
- **Access-controlled.** The backup destination is a full copy of
  tenant evidence. Lock it down to the smallest principal set: a
  dedicated backup IAM identity with write-only (`PutObject`) on the
  data prefix, no public access, bucket policy denying unencrypted
  uploads.
- **Versioned + lifecycle-bounded.** Object versioning lets you recover
  from an accidental overwrite; a lifecycle rule bounds retention (the
  maintainer instance uses 30-day rolling — see BCP/DR plan §5 Tier 3).

A minimal hardening posture on an S3-compatible destination:

```sh
# Deny any object upload that is not server-side encrypted.
# (Apply as a bucket policy on OFFSITE_BUCKET; placeholder ARNs.)
{
  "Statement": [{
    "Sid": "DenyUnencryptedUploads",
    "Effect": "Deny",
    "Principal": "*",
    "Action": "s3:PutObject",
    "Resource": "arn:aws:s3:::OFFSITE_BUCKET/*",
    "Condition": { "StringNotEquals": { "s3:x-amz-server-side-encryption": "AES256" } }
  }]
}
```

---

## Postgres — backup

The bundled `postgres` service is `postgres:16-alpine`. Take a logical
dump with `pg_dump`. Run it as a role that can read every table; on the
bundle the simplest reader is the `postgres` superuser inside the
container (the dump is a read; the _restore_ is what must be
role-correct — see the restore section).

```sh
# Variables — adjust to your deployment.
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"
STAMP="$(date -u +%Y-%m-%dT%H%M%SZ)"
DUMP="atlas-pg-${STAMP}.sql.gz"

# Dump the whole database, gzip on the way out.
$COMPOSE exec -T postgres \
  pg_dump -U postgres -d security_atlas --format=plain --no-owner \
  | gzip -c > "${DUMP}"

# Record an integrity checksum alongside the dump.
shasum -a 256 "${DUMP}" > "${DUMP}.sha256"
```

Notes:

- `--no-owner` keeps the dump portable across role-name differences
  between source and restore target. Object ownership is re-established
  by the restore (which runs as `atlas_migrate`) plus the role-bootstrap
  script.
- `-T` disables pseudo-TTY allocation so the gzip pipe stays clean.
- For an **external** (shared-cluster) Postgres, point `pg_dump` at your
  DSN directly instead of `exec`-ing into the container:
  `pg_dump "postgres://USER@db.example.internal:5432/security_atlas" ...`.

### Encrypt the backup at rest

Encrypt the dump **before** it leaves the host. `age` is a small,
auditable tool well suited to this; `gpg` works equally well.

```sh
# Encrypt to a recipient public key (the private key is held offline,
# NOT on the platform host — see Signing keys and secrets below).
age -r age1exampleplaceholderrecipientkeydonotusethisvalue00000 \
  -o "${DUMP}.age" "${DUMP}"

# Push the encrypted artifact + its checksum to the offsite store.
# (rclone remote `offsite:` configured against OFFSITE_BUCKET.)
rclone copy "${DUMP}.age"        "offsite:OFFSITE_BUCKET/postgres-dumps/"
rclone copy "${DUMP}.sha256"     "offsite:OFFSITE_BUCKET/postgres-dumps/"

# Shred the local plaintext once the encrypted copy is offsite.
rm -f "${DUMP}"
```

Even with SSE on the destination, prefer client-side encryption: SSE
protects against a stolen disk on the provider side, but the operator
controls the `age`/`gpg` key, so a compromised destination bucket still
does not yield plaintext evidence.

---

## Object store (MinIO / S3) — backup

Evidence artifacts live in the artifact bucket (default
`atlas-artifacts`). Back them up with `mc mirror`, which copies new and
changed objects.

```sh
# Configure the mc client against the running MinIO and the offsite store.
# Use a scoped backup credential, not MINIO_ROOT_*, in production.
mc alias set localminio http://localhost:9000 BACKUP_ACCESS_KEY BACKUP_SECRET_KEY
mc alias set offsite     https://s3.example.com OFFSITE_ACCESS_KEY OFFSITE_SECRET_KEY

# Mirror the artifact bucket to the offsite store.
mc mirror --overwrite localminio/atlas-artifacts offsite/OFFSITE_BUCKET/atlas-artifacts
```

For a managed S3 artifact store you can skip MinIO entirely and rely on
**cross-region replication + versioning** at the bucket level; the
mirror step is for the self-hosted MinIO case.

<!-- prettier-ignore-start -->
!!! tip "Enable SSE on the artifact bucket"
    Turn on server-side encryption for the destination bucket so the
    mirrored artifacts are encrypted at rest, and enable versioning so a
    mistaken `mc rm` is recoverable from a prior version.
<!-- prettier-ignore-end -->

---

## Signing keys and secrets

Signing keys are **not ordinary data**. Losing them breaks export
verification against a stable identity; leaking them breaks signing
integrity. Back them up **separately from the data dump, with stricter
access control**, and never in the same artifact as the evidence dump.

| Secret                                          | Where it lives                                                     | Loss impact                                                                                | Recovery posture                                                                                                                                                                  |
| ----------------------------------------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **OAuth JWT signing keystore**                  | `atlas-data` volume, `/var/lib/atlas/keys` (`ATLAS_KEYSTORE_PATH`) | Every JWT issued before the loss is invalidated; users must re-authenticate                | Back up the keystore directory; or accept re-auth and **rotate** (see [JWT key rotation](https://github.com/mgoodric/security-atlas/blob/main/docs/runbooks/jwt-key-rotation.md)) |
| **`OSCAL_SIGNING_KEY`** (embedded-ed25519 mode) | `.env` / your secret manager                                       | Prior `embedded-ed25519` OSCAL exports can no longer be verified against the same identity | Back up the hex key in your secret manager; on permanent loss, re-sign affected exports under a new key                                                                           |
| **`cosign-kms` key** (cosign-kms mode)          | In the cloud KMS, not on the host                                  | Same verification break, but the key never lived locally                                   | **Back up the KMS**, not a local file — follow your KMS provider's key backup/replication; the host holds only `ATLAS_COSIGN_KMS_REF`                                             |
| **`ATLAS_BOOTSTRAP_TOKEN`**                     | `.env`                                                             | A long-lived admin token; a leak is a privilege-escalation path                            | Treat as a first-boot convenience credential; **rotate after every restore** (see below)                                                                                          |
| **`BEARER_HASH_KEY`**                           | `.env`                                                             | API-key token hashes can no longer be validated; issued API keys break                     | Back up in the secret manager alongside the rest of `.env`                                                                                                                        |

See the [OSCAL signing runbook](https://github.com/mgoodric/security-atlas/blob/main/docs/runbooks/oscal-signing.md)
for which signing mode your deployment uses (`embedded-ed25519` is the
air-gapped default; `cosign-kms` keeps the key in a KMS).

**Handling rules:**

- Store secrets in a secret manager (or an encrypted, access-controlled
  store), **not** beside the Postgres dump. A reviewer who can read the
  data backup must not thereby gain the signing keys.
- Keep an offline encrypted copy of `OSCAL_SIGNING_KEY` so a destroyed
  host does not orphan prior exports.
- After **any** restore, rotate the bootstrap token: a restored
  deployment re-establishes its first-boot credential posture exactly as
  on a fresh install. Issue a real operator credential, then revoke the
  bootstrap token (see the [self-host bootstrap-credential note](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md#bootstrap-credential--rotate-it)).

---

## Restore

A restore brings the data back; the [drill](#tested-restore-drill)
proves it is intact. Restore is **role-correct**: schema and data
restore as `atlas_migrate` (the DDL role, `BYPASSRLS`); the running
server connects as `atlas_app` (RLS-enforced). **Never restore as a
superuser into the running app's connection** — doing so would let app
traffic bypass row-level security.

### Postgres — restore

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# 1. Stop the application so nothing writes mid-restore.
$COMPOSE stop atlas web

# 2. Pull the encrypted dump + checksum from the offsite store.
rclone copy offsite:OFFSITE_BUCKET/postgres-dumps/atlas-pg-STAMP.sql.gz.age ./
rclone copy offsite:OFFSITE_BUCKET/postgres-dumps/atlas-pg-STAMP.sql.gz.sha256 ./

# 3. Decrypt.
age -d -i /path/to/offline/age.key -o atlas-pg-STAMP.sql.gz atlas-pg-STAMP.sql.gz.age

# 4. Verify the checksum BEFORE trusting the dump.
shasum -a 256 -c atlas-pg-STAMP.sql.gz.sha256   # expect: OK

# 5. Restore the schema + data AS atlas_migrate (role-correct).
#    The three roles (atlas_migrate, atlas_app, atlas_service_account)
#    are created by migrations/bootstrap/01-roles.sql at cluster init;
#    on a fresh volume they already exist.
gunzip -c atlas-pg-STAMP.sql.gz \
  | $COMPOSE exec -T postgres psql -U atlas_migrate -d security_atlas

# 6. Bring the application back up. The app reconnects as atlas_app.
$COMPOSE up -d atlas web
```

For a **point-in-time** recovery (replay to a moment between nightly
dumps) you would need PostgreSQL WAL archival. The bundle does **not**
ship WAL archival; the honest RPO is the dump cadence (24h on the
maintainer instance). This is a named hardening item in the BCP/DR plan
§11, not a current capability.

### Object store — restore

`mc mirror` runs in the reverse direction to restore artifacts:

```sh
# Restore artifacts from the offsite store back into MinIO.
mc mirror --overwrite offsite/OFFSITE_BUCKET/atlas-artifacts localminio/atlas-artifacts
```

If the artifact store is lost but the evidence **ledger** in Postgres is
intact, the ledger is the recovery substrate: artifacts that cannot be
restored are marked artifact-lost in the ledger (the append-only
invariant forbids deleting the record), and re-ingestible evidence is
re-fetched from upstream connectors. The full procedure is BCP/DR plan
Scenario C — this runbook covers the mechanical `mc mirror` restore; the
ledger-replay path is governance-plan territory.

---

## Tested restore drill

A restore is only trustworthy if it has been exercised. Run this drill
against a **scratch stack** (a throwaway bring-up, not your live
deployment) on a cadence you are comfortable with — at minimum once
before you depend on the backups, and again whenever the schema changes
materially.

The drill maps onto the bundle's `just` recipes:
`self-host-up` → dump → `self-host-wipe` → fresh `self-host-up` →
restore → verify.

```sh
COMPOSE="docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# --- Arrange: a running, seeded stack with at least one evidence record.
just self-host-up
# wait for the bootstrap one-shot to exit 0:
$COMPOSE ps atlas-bootstrap
curl -fsS http://localhost:8080/health        # {"status":"ok","db":"ok"}

# --- Act 1: take a backup (Postgres dump; mirror MinIO if used).
$COMPOSE exec -T postgres pg_dump -U postgres -d security_atlas --no-owner \
  | gzip -c > drill.sql.gz
shasum -a 256 drill.sql.gz > drill.sql.gz.sha256

# --- Act 2: destroy everything (this is why it is a scratch stack).
just self-host-wipe        # docker compose down -v — deletes all volumes

# --- Act 3: bring up a fresh stack and restore into it.
just self-host-up
$COMPOSE stop atlas web
shasum -a 256 -c drill.sql.gz.sha256          # expect: OK
gunzip -c drill.sql.gz \
  | $COMPOSE exec -T postgres psql -U atlas_migrate -d security_atlas
$COMPOSE up -d atlas web

# --- Assert: prove the restore is faithful, not merely present.
# (1) The platform is healthy on the restored data.
curl -fsS http://localhost:8080/health        # expect {"status":"ok","db":"ok"}

# (2) A known record is present (presence check — your own query/IDs).
$COMPOSE exec -T postgres psql -U atlas_migrate -d security_atlas \
  -c "select count(*) from evidence_records;"

# (3) RLS is intact (no cross-tenant leakage post-restore).
$COMPOSE exec -T postgres psql -U atlas_migrate -d security_atlas \
  -c "select count(*) from pg_policies where schemaname = 'public';"

# (4) INTEGRITY — re-walk the restored evidence ledger and verify each
#     record's stored hash against a recomputed canonical hash. Exits
#     non-zero if any record was silently corrupted by the backup/restore.
#     Read-only; safe to run against live data.
atlas evidence verify --all-tenants            # expect: mismatches=0, exit 0

# (5) INTEGRITY (cryptographic, end-to-end) — a signed OSCAL export still
#     verifies on the restored data. Export a frozen audit period, then
#     verify the bundle's signature. This exercises the cosign/ed25519
#     verification path end-to-end and proves the evidence behind the
#     export is intact.
atlas oscal-export --period <frozen-period-id> --out ./drill-bundle
atlas oscal verify ./drill-bundle             # expect: signature OK
```

Steps (4) and (5) are the load-bearing assertions: **integrity, not just
presence.** A silently-corrupt backup might restore "successfully" with
damaged evidence; the ledger-wide `evidence verify` walk catches a mutated
record by recomputing its canonical hash, and the signed-export verify
additionally catches damage end-to-end via a sha256 over the member bytes
checked against the signature — both kinds of damage a row count would miss.

<!-- prettier-ignore-start -->
!!! note "Evidence-record integrity (verify surfaces)"
    Per-record sha256 integrity is computed and stored at ingest.
    `atlas evidence verify` re-walks the ledger on demand and reports any
    record whose stored hash no longer matches a recomputed canonical hash
    — a tenant-scoped walk (`--tenant <uuid>`, as `atlas_app` under RLS) or
    a cross-tenant walk (`--all-tenants`, as `atlas_service_account`). The
    signed-OSCAL-export verify above is the complementary cryptographic,
    end-to-end check. Run both in a restore drill.
<!-- prettier-ignore-end -->

### Record the drill

Per the BCP/DR plan §10, a real restore is a continuity event and should
be recorded operationally (incident log entry; backup artifact filename

- hash; outcome) so the audit trail reflects it. A drill is not an
  incident, but recording the date and outcome of each drill gives you the
  "when did we last prove this works?" answer a diligence reviewer asks.

---

## How this maps to the BCP/DR plan

| BCP/DR plan element                                                   | This runbook                                                                                  |
| --------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| §5 Tier 3 backup strategy (Postgres nightly, object-store versioning) | The backup procedures above are the operator-side mechanics                                   |
| §6 Scenario B (Postgres corruption restore)                           | [Postgres restore](#postgres-restore) — same `atlas_migrate` role-correct path                |
| §6 Scenario C (object-store loss)                                     | [Object store restore](#object-store-restore) + the ledger-replay note                        |
| §6 Scenario D/E (key compromise)                                      | [Signing keys and secrets](#signing-keys-and-secrets) — separate backup, rotate-after-restore |
| §11 hardening: WAL archival                                           | Noted as not-shipped; RPO is the dump cadence                                                 |

For upgrades (pin a version, pre-upgrade checkpoint, `migrate up`,
rollback) see [Upgrade](upgrade.md). For the full self-host bring-up see
[Install](install.md).
