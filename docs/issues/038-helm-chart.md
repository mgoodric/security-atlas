# 038 — Helm chart for K8s deployment

**Cluster:** Infra / deploy
**Estimate:** 2d
**Type:** AFK

## Narrative

Ship the Helm chart that deploys security-atlas to a Kubernetes cluster. Chart bundles: atlas server Deployment, frontend Deployment, NATS StatefulSet, Postgres external dependency (recommended via cloud provider or operator), MinIO/S3 external dependency. `values.yaml` exposes: image tags, replica counts, OIDC config, TLS certs, RLS / tenancy mode. Helm hooks run migrations as a pre-install Job. The slice delivers value because production-grade deployments are an `helm install` away — no bespoke YAML stitching.

## Acceptance criteria

- [ ] AC-1: `helm install security-atlas ./deploy/helm` deploys to a minikube cluster successfully
- [ ] AC-2: Pre-install hook runs Atlas migrations against a configured Postgres
- [ ] AC-3: `values.yaml` documented; sane defaults for solo deployments
- [ ] AC-4: `values-production.yaml` template demonstrates the production shape (multiple replicas, external Postgres, real S3, real IdP)
- [ ] AC-5: Helm lint + helm template both pass in CI
- [ ] AC-6: Ingress + cert-manager integration documented (cert-manager optional)
- [ ] AC-7: `helm upgrade` between versions runs migrations idempotently

## Constitutional invariants honored

- **Replacement-grade criterion 6 (multi-tenant isolation in self-host):** chart provides the production deployment shape that survives third-party security review

## Canvas references

- `Plans/canvas/09-tech-stack.md` (Helm chart for K8s)
- `Plans/canvas/10-roadmap.md` §10.1 (Self-host row)

## Dependencies

- #037

## Anti-criteria (P0)

- Does NOT bundle a Postgres operator (Postgres is external — supports RDS, Aurora, CrunchyData, etc.)
- Does NOT skip the migration hook
- Does NOT ship without lint passing in CI

## Skill mix (3–5)

- Helm chart authoring
- Kubernetes deployments + StatefulSets
- Helm hooks + migration jobs
- cert-manager / Ingress
- values-driven configuration design
