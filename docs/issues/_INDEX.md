# v1 Issue Index — Tracer-Bullet Vertical Slices

**Total:** 49 slices · **~90 day-equivalents** · **Critical path:** 11 slices (~24 days)
**Spine:** 001–005 honor CLAUDE.md "When code begins" ordering.
**Status:** Ready for review — no code committed yet.

> Tracer-bullet slicing: each slice cuts vertically through every layer (schema, API, UI, tests) — *not* horizontally through one layer. A completed slice is demoable on its own.

## Reading

- **Topological order below** — start at #001 and proceed sequentially for a serial build. Parallel teams should pick from any slice whose deps are satisfied.
- **HITL** = Human-in-the-loop required (review or domain validation before merge). **AFK** = Can be implemented and merged without human interaction in the implementation path.
- See [`_DEPENDENCY_GRAPH.md`](./_DEPENDENCY_GRAPH.md) for the mermaid visualization of dependencies.

## Topological order

| # | Title | Cluster | Type | Est. (d) | Deps | Status |
|---|---|---|---|---|---|---|
| [001](./001-monorepo-skeleton.md) | Monorepo skeleton + CI green build | Spine | AFK | 1.5 | — | Ready |
| [002](./002-schema-migrations.md) | Schema + migrations (6 primitives + FrameworkScope + tenancy plumbing) | Spine | AFK | 3 | 001 | Ready |
| [003](./003-evidence-sdk-proto-push-client-cli.md) | Evidence SDK: proto + Go push client + CLI | Spine | AFK | 2.5 | 001 | Ready |
| [014](./014-schema-registry-service.md) | Schema registry service (in-tree Go) | Evidence pipeline | AFK | 1.5 | 002 | Ready |
| [006](./006-scf-catalog-importer.md) | SCF catalog importer + Framework/FrameworkVersion API | Catalog | AFK | 2 | 002 | Ready |
| [009](./009-control-bundle-format.md) | Control bundle format spec + parser + upload | Control-as-code | AFK | 2 | 002 | Ready |
| [017](./017-scope-dimensions-applicability.md) | Scope dimensions + applicability_expr + default single-cell seed | Scope | AFK | 2 | 002 | Ready |
| [019](./019-risk-register-crud.md) | Risk CRUD + NIST 800-30 + 5x5 + ALE-band | Risk register | AFK | 2 | 002 | Ready |
| [022](./022-policy-library.md) | Policy library + 5 stock policies | Policies | HITL | 2 | 002 | Ready |
| [033](./033-postgres-rls-enforcement.md) | Postgres RLS enforcement everywhere | Multi-tenancy | AFK | 2 | 002 | Ready |
| [034](./034-oidc-rp-local-users.md) | OIDC RP + local users | Auth | AFK | 1.5 | 001 | Ready |
| [013](./013-evidence-ledger-write-api.md) | Evidence ledger write API + push endpoint | Evidence pipeline | AFK | 3 | 002, 003, 014 | Ready |
| [007](./007-soc2-crosswalk-loader.md) | SOC 2 v2017 (TSC) crosswalk loader | Catalog | HITL | 1.5 | 006 | Ready |
| [018](./018-framework-scope-intersection.md) | FrameworkScope predicate + intersection compute | Scope | AFK | 1.5 | 017 | Ready |
| [024](./024-vendor-lite-module.md) | Vendor lite module | Vendor | AFK | 1.5 | 002, 017 | Ready |
| [015](./015-nats-jetstream-ingestion-stage.md) | NATS JetStream buffer + ingestion stage | Evidence pipeline | AFK | 2 | 013 | Ready |
| [036](./036-s3-artifact-store.md) | S3 artifact store integration | Infra | AFK | 1 | 013 | Ready |
| [004](./004-aws-connector-s3-encryption.md) | AWS connector (S3 encryption, end-to-end) | Spine | AFK | 3 | 002, 003, 013, 014 | Ready |
| [035](./035-rbac-abac-opa.md) | RBAC roles + ABAC via OPA embedded | Auth | HITL | 2 | 033, 034 | Ready |
| [023](./023-policy-acknowledgment.md) | Policy acknowledgment workflow | Policies | AFK | 1 | 022, 034 | Ready |
| [044](./044-github-connector.md) | GitHub connector | Connectors | AFK | 1 | 003, 013 | Ready |
| [045](./045-okta-connector.md) | Okta connector | Connectors | AFK | 1 | 003, 013 | Ready |
| [046](./046-1password-connector.md) | 1Password connector | Connectors | AFK | 0.5 | 003, 013 | Ready |
| [047](./047-osquery-fleet-connector.md) | osquery/Fleet endpoint connector | Connectors | AFK | 1 | 003, 013 | Ready |
| [048](./048-jira-linear-connector.md) | Jira/Linear ticket connector | Connectors | AFK | 1 | 003, 013 | Ready |
| [049](./049-manual-upload-csv-connector.md) | Manual upload / CSV / S3 / SFTP escape-hatch | Connectors | AFK | 1 | 003, 013 | Ready |
| [008](./008-ucf-graph-traversal-api.md) | UCF graph traversal query API | Catalog | AFK | 2 | 002, 006, 007 | Ready |
| [010](./010-soc2-control-kit.md) | SCF-anchored control kit (50 SOC 2 controls) | Control-as-code | HITL | 3 | 009, 007 | Ready |
| [011](./011-manual-control-attestation.md) | Manual control type + attestation flow | Control-as-code | AFK | 1.5 | 009, 013, 036 | Ready |
| [025](./025-auditor-role-scoped-access.md) | Auditor role + scoped read-only access | Audit | AFK | 1.5 | 033, 035 | Ready |
| [005](./005-frontend-bootstrap.md) | Frontend bootstrap (Next.js + auth + SCF browser) | Spine | AFK | 2 | 001, 008 | Ready |
| [012](./012-control-state-evaluation.md) | Control state evaluation engine | Control-as-code | AFK | 2.5 | 010, 013, 017 | Ready |
| [039](./039-cli-release-pipeline.md) | CLI binary distribution + release pipeline | Infra | AFK | 1 | 001, 003 | Ready |
| [026](./026-sample-pull-primitives.md) | Sample-pull primitives (Population + Sample) | Audit | AFK | 1.5 | 013, 017 | Ready |
| [027](./027-walkthrough-recording.md) | Walkthrough recording (annotated + hash/sign) | Audit | AFK | 2 | 025, 036 | Ready |
| [029](./029-audit-hub-comments.md) | Audit Hub threaded comments | Audit | AFK | 1.5 | 025 | Ready |
| [016](./016-evidence-freshness-drift.md) | Evidence freshness + drift detection | Evidence pipeline | AFK | 1.5 | 012 | Ready |
| [020](./020-risk-control-linkage-residual.md) | Risk → control linkage + residual derivation | Risk register | AFK | 2 | 019, 012 | Ready |
| [021](./021-exception-waiver-workflow.md) | Exception/waiver workflow + auto-expiry | Risk register | AFK | 1.5 | 019, 017 | Ready |
| [028](./028-audit-period-freezing.md) | AuditPeriod + freezing primitive | Audit | AFK | 2 | 013, 016 | Ready |
| [030](./030-oscal-ssp-poam-export.md) | OSCAL SSP + POA&M export pipeline | Audit | HITL | 3 | 008, 012, 017, 018, 026, 028 | Ready |
| [031](./031-monthly-board-brief.md) | Monthly board brief (templated, no LLM) | Board | AFK | 1.5 | 012, 016, 020 | Ready |
| [037](./037-docker-compose-self-host.md) | docker-compose self-host bundle | Infra | AFK | 1.5 | 002, 013, 034 | Ready |
| [038](./038-helm-chart.md) | Helm chart for K8s | Infra | AFK | 2 | 037 | Ready |
| [040](./040-program-dashboard-view.md) | Program dashboard view | Frontend | AFK | 2.5 | 005, 012, 016, 020, 024 | Ready |
| [041](./041-control-detail-view.md) | Control detail view + UCF mini-viz | Frontend | AFK | 3 | 005, 008, 012 | Ready |
| [042](./042-audit-workspace-view.md) | Audit workspace view (sample + walkthrough + comments) | Frontend | AFK | 2.5 | 025, 026, 027, 029 | Ready |
| [032](./032-quarterly-board-pack.md) | Quarterly board pack + investment-vs-coverage | Board | AFK | 2.5 | 031, 030 | Ready |
| [043](./043-board-pack-preview-view.md) | Board pack preview/export view | Frontend | AFK | 2 | 005, 032 | Ready |

## Effort by cluster

| Cluster | Slices | Days |
|---|---|---|
| Spine | 5 | 12 |
| Catalog & UCF graph | 3 | 5.5 |
| Control-as-code | 4 | 9 |
| Evidence pipeline | 4 | 8 |
| Scope + FrameworkScope | 2 | 3.5 |
| Risk register | 3 | 5.5 |
| Policies | 2 | 3 |
| Vendor lite | 1 | 1.5 |
| Audit workflow | 6 | 11.5 |
| Board reporting | 2 | 4 |
| Multi-tenancy / auth | 3 | 5.5 |
| Infra / deploy | 4 | 5.5 |
| Frontend views | 4 | 10 |
| Remaining connectors | 6 | 5.5 |
| **Total** | **49** | **~90** |

## Critical path (longest dependency chain)

11 slices · ~24 day-equivalents:

```
001 skeleton → 002 schema → 006 SCF importer → 007 SOC 2 crosswalk →
010 50 controls bundled → 012 control state eval → 016 freshness →
028 audit-period freeze → 030 OSCAL export → 032 quarterly pack →
043 board pack view
```

This path threads the binary v1 success test: real evidence flows → controls evaluate → audit cycle → board pack.

## Maximum parallelism

After slices 001 + 002 land (~4.5 days in), up to **10 streams** can run in parallel:

003 · 006 · 009 · 014 · 017 · 019 · 022 · 024 · 033 · 034 · (+ 005 after 008)

Realistic at 3-dev team: 3 sustained parallel streams.

## Open questions tracked

See `Plans/canvas/11-open-questions.md` for the full list. Items most likely to surface as decisions during v1 implementation:

- **#01 SCF licensing fine print** — must clear before slice 006 release bundle
- **#13 Solo-operator vs multi-tenant tension** — UX-level decision affecting slices 033, 034, 037
- **#19 FrameworkScope ownership workflow** — UX design affects slice 018
- **#17 Schema-registry governance** — affects slice 014's community contribution flow (likely deferred to v2)
- **#18 Push credential issuance UX** — affects slice 035's API-key UX

## Quality gates self-check

- ✅ Every slice has integration-test-shaped acceptance criteria
- ✅ Every slice cites at least one canvas section
- ✅ Every slice cites at least one constitutional invariant (or anti-pattern rejection)
- ✅ Every slice has explicit dependency list
- ✅ Every slice has anti-criteria block
- ✅ Every slice estimate in 0.5–3 day range
- ✅ Every slice names 3–5 skill mix
- ✅ Dependency graph is a DAG (verified)
- ✅ Spine ordering honored (001 → 002 precedes 003 precedes 004 + 013; 005 follows 008)
- ✅ No slice drifts into phase 2/3 (ISO/PCI/HIPAA/GDPR mappings, AI-assist drafting, trust center, ClickHouse, plugin marketplace, OSCAL ingest)

## Mode markers

- 5 slices are **HITL** (require human review): 007 (mapping spot-check), 010 (control accuracy), 022 (stock policy text), 035 (role+policy review), 030 (auditor partner SSP validation)
- 44 slices are **AFK**

## What's not in v1 (explicitly deferred to phase 2/3)

Per `Plans/canvas/10-roadmap.md` §10.2–10.3:
- Framework versions beyond SOC 2:2017 (ISO 27001:2022, NIST CSF 2.0, PCI DSS v4.0, HIPAA, GDPR) → phase 2
- AI-assisted drafting (questionnaire answers, board narrative LLM polish, policy text) → phase 2
- Trust center → phase 3
- ClickHouse analytics path → phase 3
- Plugin marketplace → phase 3
- OSCAL catalog/profile **ingest** → phase 3 (FedRAMP-driven)
- Connectors beyond v1 roster → phase 2
- Multi-org / portfolio view → phase 3
