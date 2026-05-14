# security-atlas Helm chart

Deploys security-atlas to a Kubernetes cluster: the atlas server, the web
frontend, NATS JetStream, an optional bundled MinIO, and a pre-install Job
that runs the database migrations. **Postgres is an external dependency** —
the chart never bundles a database (see [External Postgres](#external-postgres)).

This is the Kubernetes leg of the canvas §10.1 "Self-host" row. The
single-VM leg is `deploy/docker/` (docker-compose, slice 037).

|               |                               |
| ------------- | ----------------------------- |
| Chart version | see `Chart.yaml` `version`    |
| App version   | see `Chart.yaml` `appVersion` |
| Slice         | 038                           |

## Quick start (solo / dev)

The default values target a single-node cluster (minikube / kind / k3s):
single replicas, bundled MinIO, inline placeholder secrets, no Ingress.

```sh
# 1. You need an external Postgres reachable from the cluster. For a
#    throwaway dev cluster, the fastest path is an in-cluster Postgres you
#    manage yourself (the chart does NOT do this for you):
kubectl create namespace security-atlas
kubectl -n security-atlas run pg --image=postgres:16-alpine \
  --env=POSTGRES_PASSWORD=devpw --env=POSTGRES_DB=security_atlas \
  --port=5432 --expose

# 2. Install, overriding the placeholder credentials. The migration role
#    on a fresh dedicated Postgres needs superuser-equivalent rights to
#    create the atlas_* roles; for dev, point migrateUser at `postgres`.
helm install security-atlas ./deploy/helm \
  --namespace security-atlas \
  --set postgres.host=pg \
  --set postgres.migrateUser=postgres \
  --set secrets.postgresMigratePassword=devpw \
  --set secrets.postgresAppPassword="$(openssl rand -hex 16)" \
  --set secrets.bearerHashKey="$(openssl rand -hex 32)" \
  --set secrets.bootstrapToken="$(openssl rand -hex 32)" \
  --set secrets.defaultUserPassword="$(openssl rand -hex 16)" \
  --set secrets.minioRootUser=atlasminio \
  --set secrets.minioRootPassword="$(openssl rand -hex 16)"

# 3. Reach the UI.
kubectl -n security-atlas port-forward svc/security-atlas-web 3000:3000
```

For anything beyond a throwaway cluster, use a pre-created Secret — see
[Secrets](#secrets) — and the [production values](#production).

## What the chart deploys

| Object                     | Kind                                     | Notes                                                                                                                        |
| -------------------------- | ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `<release>-migrate`        | Job (`pre-install`,`pre-upgrade` hook)   | Runs `bootstrap.sh`: migrations + roles + seed + SCF import + control upload. Idempotent via the `schema_migrations` ledger. |
| `<release>-atlas`          | Deployment + Service                     | Platform server. gRPC :50051 + HTTP :8080. Stateless — scales via `atlas.replicaCount`.                                      |
| `<release>-web`            | Deployment + Service                     | Next.js frontend. :3000. Stateless — scales via `web.replicaCount`.                                                          |
| `<release>-nats`           | StatefulSet + headless Service           | NATS JetStream. `volumeClaimTemplate` persists stream state.                                                                 |
| `<release>-minio`          | Deployment + Service (+ PVC)             | **Optional** bundled artifact store. `minio.enabled`, default `true`. Disable for production + external S3.                  |
| `<release>-minio-bucket`   | Job (`post-install`,`post-upgrade` hook) | Creates the artifacts bucket in the bundled MinIO. Only when `minio.enabled`.                                                |
| `<release>-config`         | ConfigMap                                | Non-secret env (addresses, bucket, region).                                                                                  |
| `<release>-secrets`        | Secret                                   | Inline-rendered credentials — only when `secrets.existingSecret` is unset.                                                   |
| `<release>`                | Ingress                                  | **Optional**. `ingress.enabled`, default `false`.                                                                            |
| `<release>-security-atlas` | ServiceAccount                           | `serviceAccount.create`, default `true`.                                                                                     |

## Values reference

Every key is documented inline in [`values.yaml`](./values.yaml). The
high-traffic ones:

| Key                                                  | Default                                                     | Purpose                                                              |
| ---------------------------------------------------- | ----------------------------------------------------------- | -------------------------------------------------------------------- |
| `image.registry` / `image.repository` / `image.tag`  | `ghcr.io` / `mgoodric/security-atlas` / `""` (= appVersion) | First-party image coordinates.                                       |
| `atlas.replicaCount` / `web.replicaCount`            | `1` / `1`                                                   | Stateless-tier scale-out.                                            |
| `postgres.host` / `.port` / `.database` / `.sslmode` | `postgres` / `5432` / `security_atlas` / `disable`          | External Postgres connection. Use `sslmode: require`+ in production. |
| `postgres.appUser` / `.migrateUser`                  | `atlas_app` / `atlas_migrate`                               | The two roles (slice 065 role model).                                |
| `minio.enabled`                                      | `true`                                                      | Bundle MinIO (dev) or use external S3 (production → `false`).        |
| `artifacts.s3Endpoint`                               | `""`                                                        | **Required** when `minio.enabled: false`.                            |
| `oidc.issuer` / `.clientId` / `.redirectUrl`         | `""`                                                        | OIDC relying-party config. Empty issuer = local-user mode.           |
| `secrets.existingSecret`                             | `""`                                                        | Name of a pre-created Secret. Set this for production.               |
| `secrets.*` (inline)                                 | `changeme`                                                  | Placeholder credentials — used only when `existingSecret` is unset.  |
| `ingress.enabled`                                    | `false`                                                     | Render an Ingress.                                                   |
| `ingress.certManager.enabled`                        | `false`                                                     | Add the cert-manager cluster-issuer annotation + TLS block.          |

## Secrets

The chart supports two credential modes.

### Mode 1 — `existingSecret` (production)

Pre-create a Kubernetes Secret out-of-band, then set
`secrets.existingSecret: <name>`. The chart renders **no** Secret object and
every workload reads from yours by name. This keeps real credentials out of
values files and Helm release history. Prefer managing the Secret with
External Secrets Operator, Sealed Secrets, or SOPS.

The Secret must carry these keys:

| Key                                           | Used by            | Notes                                                                      |
| --------------------------------------------- | ------------------ | -------------------------------------------------------------------------- |
| `DATABASE_URL_APP`                            | atlas              | RLS-enforced app-role DSN.                                                 |
| `DATABASE_URL_MIGRATE`                        | atlas, migrate Job | BYPASSRLS atlas_migrate DSN.                                               |
| `ATLAS_APP_PASSWORD`                          | migrate Job        | Sets the atlas_app role password.                                          |
| `BEARER_HASH_KEY`                             | atlas              | HMAC key for `api_keys.token_hash`. `openssl rand -hex 32`.                |
| `ATLAS_BOOTSTRAP_TOKEN`                       | atlas, migrate Job | Pre-shared admin token for control-bundle upload. Rotate after first boot. |
| `ATLAS_DEFAULT_USER_PASSWORD`                 | migrate Job        | Seeded local user password.                                                |
| `ATLAS_OIDC_CLIENT_SECRET`                    | atlas              | Only when `oidc.issuer` is set.                                            |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | atlas              | S3 credentials. Omit if using IRSA / workload identity.                    |

### Mode 2 — inline (dev convenience)

Leave `secrets.existingSecret` empty and the chart renders a Secret named
`<release>-security-atlas-secrets` from the `secrets.*` values. Every
default is the literal placeholder `changeme` — override them all via
`--set` or a private `-f` values file. **Throwaway clusters only.**

## External Postgres

The chart bundles **no** database — Postgres is yours to run (RDS, Aurora,
CrunchyData / Zalando operator, a managed cloud DB, anything that speaks
the Postgres wire protocol).

The migration Job connects as `atlas_migrate` and applies
`migrations/bootstrap/01-roles.sql` (baked into the bootstrap image). On a
**dedicated** Postgres with trust auth, `atlas_migrate` effectively stands
in for the superuser and that script runs clean. On a **shared** cluster
where you pre-create `atlas_migrate` as a non-superuser, the cluster admin
must first run, as a superuser (slice 065 bug #4 / #6):

```sql
ALTER ROLE atlas_migrate BYPASSRLS CREATEROLE;
ALTER SCHEMA public OWNER TO atlas_migrate;
```

See `migrations/bootstrap/01-roles.sql` for the full rationale.

## Ingress and cert-manager

`ingress.enabled: true` renders an Ingress that fronts both the web
frontend and the atlas HTTP API under one host:

- `/` → web Service (the browser app)
- `/v1`, `/health`, `/auth` (configurable via `ingress.paths.apiPrefixes`)
  → atlas Service

Because both are one host, `web.publicApiBaseUrl` can stay empty — the
browser uses same-origin relative URLs.

TLS is opt-in, two ways:

1. **cert-manager** — set `ingress.certManager.enabled: true` and
   `ingress.certManager.clusterIssuer`. The chart adds the
   `cert-manager.io/cluster-issuer` annotation and a TLS block;
   cert-manager provisions a certificate into `<host>-tls`. cert-manager
   must already be installed in the cluster — the chart does not install
   it.
2. **Bring your own** — set `ingress.tls.existingSecret` to a TLS Secret
   you manage.

With neither, the Ingress is plain HTTP (dev only).

## Production

[`values-production.yaml`](./values-production.yaml) is an annotated
template demonstrating the production shape: multiple replicas, external
Postgres, external S3 (`minio.enabled: false`), a real OIDC IdP, an Ingress
with cert-manager TLS, and `existingSecret`.

```sh
helm install security-atlas ./deploy/helm \
  --namespace security-atlas --create-namespace \
  -f deploy/helm/values-production.yaml \
  -f my-private-overrides.yaml
```

## Upgrades

`helm upgrade` re-runs the migration Job as a `pre-upgrade` hook. The
`schema_migrations` ledger (slice 065) records each applied migration's
basename and skips it on re-run, so the Job applies **only** new
migrations — idempotently. Pin `image.tag` to the platform release you are
upgrading to.

## Testing the chart

`helm lint` + `helm template` are the chart's test surface and run in CI
(`.github/workflows/ci.yml`, job `Helm chart · lint + template`):

```sh
helm lint deploy/helm
helm lint deploy/helm -f deploy/helm/values-production.yaml
helm template t deploy/helm
helm template t deploy/helm -f deploy/helm/values-production.yaml
```

### Manual minikube integration check

A full `helm install` against a live cluster is a manual check (CI does not
spin up a Kubernetes node). To exercise it end-to-end on minikube:

```sh
minikube start
# Bring up an in-cluster Postgres for the check (the chart does not):
kubectl create namespace security-atlas
kubectl -n security-atlas run pg --image=postgres:16-alpine \
  --env=POSTGRES_PASSWORD=devpw --env=POSTGRES_DB=security_atlas \
  --port=5432 --expose
# Install (see Quick start for the full --set list):
helm install security-atlas ./deploy/helm -n security-atlas \
  --set postgres.host=pg --set postgres.migrateUser=postgres \
  --set secrets.postgresMigratePassword=devpw \
  --set secrets.postgresAppPassword=devapppw \
  --set secrets.bearerHashKey=$(openssl rand -hex 32) \
  --set secrets.bootstrapToken=$(openssl rand -hex 32) \
  --set secrets.defaultUserPassword=devuserpw \
  --set secrets.minioRootUser=atlasminio \
  --set secrets.minioRootPassword=atlasminiopw
# Verify the migration hook ran and the pods are healthy:
kubectl -n security-atlas get jobs,pods
kubectl -n security-atlas logs job/security-atlas-migrate
kubectl -n security-atlas port-forward svc/security-atlas-web 3000:3000
```

Expected: the `security-atlas-migrate` Job completes, the atlas / web /
nats / minio pods reach `Running`, and the UI loads on `localhost:3000`.
