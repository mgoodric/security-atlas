**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 9. Architecture and Tech Stack

Opinionated. One choice per slot, defended in one paragraph.

## 9.1 Backing data store

**PostgreSQL 16+ (primary) + S3-compatible object store (artifacts) + ClickHouse (evidence ledger analytics, optional v2).**

Postgres because: row-level security, JSONB flexibility for evolving evidence payloads, mature operational tooling, and you can run it anywhere from a Pi to RDS. Object store for evidence artifacts that exceed 1 MB. ClickHouse only when evidence-record volume crosses ~10⁹ — added behind a read-model interface so v1 doesn't depend on it.

Reject: Neo4j as primary (the graph is not big enough to need it; we use Postgres with `ltree` and recursive CTEs, or `pg_graph` extensions, for graph traversals). Reject: Mongo (schemaless evidence is a bug, not a feature; provenance demands schema).

## 9.2 Backend language/runtime

**Go for the platform core; Python for the connector SDK reference implementation; community connectors in any language over the gRPC contract.**

Go because: static binary deploy, low operational overhead for self-host, strong concurrency for evidence-stream consumers, mature OSCAL bindings via compliance-trestle interop (which is Python — so we ship a stable gRPC bridge for it). Python for connectors because the data-engineering ergonomics dominate.

## 9.3 Event/queue layer

**NATS JetStream for v1.**

JetStream gives us durable streams, key-value, and object store in one binary, with at-least-once delivery and stream replay (critical for evidence reprocessing). Self-host is one binary. Cloud is straightforward. Reject: Kafka (operational overhead), SQS (vendor lock for self-host), Redis Streams (no durability guarantees we want).

## 9.4 Plugin architecture

Three extension surfaces, narrow on purpose:

| Surface           | What you can extend     | Mechanism                                         |
| ----------------- | ----------------------- | ------------------------------------------------- |
| Connector         | New evidence sources    | gRPC contract per [§4.1](./04-evidence-engine.md) |
| Control bundle    | New controls / mappings | Versioned bundle uploaded to a registry; signed   |
| Notification sink | Where alerts/digests go | Webhook + a small handful of native sinks         |

That's it for v1. No "plugin everything" surface. Plugins are installed per-deployment, not per-tenant, in v1 — keeps the security model simple. Per-tenant plugin marketplaces are a v3 conversation.

## 9.5 Auth model

**OIDC for authentication, internal OAuth 2.0 Authorization Server for token issuance, RBAC + ABAC for authorization.**

OIDC because every credible IdP speaks it; we ship as a relying party only, never as an IdP. atlas-as-OIDC-RP authenticates the human via the external IdP; the atlas-AS layer (RFC 9068 JWT Profile · RFC 8693 Token Exchange · RFC 7636 PKCE · RFC 8628 Device Authorization Grant · RFC 7009 Revocation · RFC 7662 Introspection) mints the atlas JWT access tokens that carry tenant-in-claim. The slice 187 foundation ships the JWT signing keypair, JWKS endpoint, OIDC discovery doc, and the JWT claim types; the rest of the spine (188-192) ships the grant flows, JWT validation middleware, frontend OAuth client, SDK migration, and multi-tenant tenant-switch. See [`docs/adr/0003-oauth-authorization-server.md`](../../docs/adr/0003-oauth-authorization-server.md) for the architectural rationale and [`Plans/canvas/11-open-questions.md`](./11-open-questions.md) item 21 for the resolution context.

RBAC for coarse roles (`admin`, `grc_engineer`, `control_owner`, `auditor`, `viewer`). ABAC for the fine cuts that matter (`auditor X can only see scope cells within audit_period Y for client Z`). Authorization decisions live in OPA — same engine that evaluates control policies, so the security model is auditable in the same substrate as the controls.

## 9.6 CI/CD

**GitHub Actions; `dorny/paths-filter@v3` is the in-workflow gate for docs-only PRs (slice 061).**

A `changes` job runs first in `ci.yml`, sets a `code` boolean, and each expensive job is paired with a same-named stub sibling so branch-protection required-check names always resolve. As of 2026-05-15 the path-filtered jobs are: `Go · build + test`, `Go · integration (Postgres RLS)`, `Go · lint`, `Frontend · install + build`, `Frontend · vitest`, `Frontend · lint` (slice 078), `Frontend · Playwright e2e` (slice 069, quarantined per slice 079), `Python · ruff`, `Proto · lint + format`, `OSCAL bridge · pytest`, `Self-host bundle · smoke`, `Helm chart · lint + template`, `Go · govulncheck` (slice 089/090), `Frontend · npm audit` (slice 089), `Container · Trivy scan` (slice 089). Security and secret-scan jobs (CodeQL, GitGuardian, pre-commit) are always-on and never gated by paths. See [`docs/ci/PATH_FILTERING.md`](../../docs/ci/PATH_FILTERING.md) for the rationale and the `paths-ignore:`-at-workflow-level gotcha.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 8. Audit Workflow](./08-audit-workflow.md) · **Next:** [10. Roadmap →](./10-roadmap.md)
