# deploy/

Deployment artifacts.

| Path             | Purpose                                                                                                            | Slice |
| ---------------- | ------------------------------------------------------------------------------------------------------------------ | ----- |
| `deploy/docker/` | `docker-compose.yml` + Dockerfiles for the self-host bundle (Postgres + NATS + MinIO + atlas + frontend on one VM) | 037   |
| `deploy/helm/`   | Helm chart for Kubernetes deployments                                                                              | 038   |

Both target the same core platform; docker-compose is for solo self-host and the 4-hour-to-first-evidence acceptance criterion (slice 037), while Helm is for production SaaS deployments (slice 038).

`deploy/docker/` and `deploy/helm/` are both populated. `deploy/helm/` ships the Helm chart (atlas server + web frontend + NATS StatefulSet + optional bundled MinIO + a pre-install migration Job); Postgres is an external dependency. See [`deploy/helm/README.md`](./helm/README.md) for the values reference, the secrets model, external-Postgres setup, and Ingress / cert-manager integration.
