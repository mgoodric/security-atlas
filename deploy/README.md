# deploy/

Deployment artifacts.

| Path | Purpose | Slice |
|---|---|---|
| `deploy/docker/` | `docker-compose.yml` + Dockerfiles for the self-host bundle (Postgres + NATS + MinIO + atlas + frontend on one VM) | 037 |
| `deploy/helm/` | Helm chart for Kubernetes deployments | 038 |

Both target the same core platform; docker-compose is for solo self-host and the 4-hour-to-first-evidence acceptance criterion (slice 037), while Helm is for production SaaS deployments (slice 038).

Empty in slice 001.
