**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 10. Roadmap and Sequencing

## 10.1 MVP (Phase 1) — "Solo operator, one framework, real audit, board-ready"

**Goal:** Matt — solo security leader at a 70-person security-product startup — installs security-atlas, runs his entire SOC 2 program from it, generates the quarterly board pack from it, and survives the audit. Setup-to-first-evidence in under 4 hours. No consulting hours. The single test of the v1 hypothesis.

**Scope (deliberately tight):**

| Area | v1 contents | Notes |
|---|---|---|
| Catalog | SCF ingested + SOC 2 v2017 (TSC) crosswalked | One framework crosswalked is enough to prove the graph works; the spine is in place for phase 2. |
| Connectors | 7 high-leverage: AWS, GitHub, Okta or Google Workspace, 1Password, osquery/Fleet, Jira/Linear, manual-upload/CSV | These cover ~70% of evidence demand at a SaaS startup. Ship deeply, not broadly. |
| Control-as-code | Authoring kit + ~50 SOC 2 controls bundled | Stock controls usable as-is or forkable. |
| Evidence engine | Append-only ledger, ingestion + evaluation separation, freshness model | Hybrid event-driven (where APIs allow) + query-driven snapshots. |
| Scope | Dimensions defined (BU, env, cloud, data class) but a default single-cell org works | Don't force scope modeling on day one. |
| Risk register | NIST 800-30 default, 5x5 qualitative + dollar-banded impact, FAIR for top-N | Methodology pluggable; default reflects what practitioners actually use. |
| Policy library | 5 high-signal stock policies (Information Security, Access Control, Vendor Management, Incident Response, Change Management) + acknowledgment workflow | Not 50 placeholder docs. Each owned, dated, version-controlled. |
| Vendor module | Lite — vendor entity, contract dates, DPA status, review cadence, criticality, last-review-date | The minimum to retire the vendor spreadsheet. |
| Audit workflow | Auditor role + sample-pull + walkthrough + audit-period freezing + Audit Hub comments + OSCAL SSP export | All five primitives or none — they compose. |
| Board reporting | Monthly brief + quarterly pack with auto-drafted narrative + investment-vs-coverage manual entry | First-class. Validated by user delivering an actual board pack from the tool. |
| Multi-tenancy | Postgres RLS from day one (even in single-tenant deployments) | Get the path right early. |
| Self-host | One-binary core + NATS (single binary) + Postgres (single instance) + S3-compatible artifact store. Helm chart + docker-compose. | Must run on a single mid-size VM. |
| Auth | OIDC RP for SSO; local users for solo deployments | RBAC roles: admin, grc_engineer, control_owner, auditor, viewer. |

**Deliberately deferred from MVP:** trust center, ClickHouse path, per-tenant plugin install, framework versions beyond SOC 2:2017, TPRM workflow beyond the lite module, training/phishing connector (use manual upload), AI assistance on policy text, GDPR-specific privacy module, HIPAA-specific covered-entity workflow.

**The v1 success test is binary:** does Matt run his next SOC 2 audit out of security-atlas, generate his next board pack from it, and not reach for Vanta or a Google Sheet to fill a gap? If yes, v1 is done.

## 10.2 Phase 2 — "The mapping engine pays off"

- Add framework versions: ISO 27001:2022 (ISO 27001 is most likely Matt's next audit, prospect-driven), NIST CSF 2.0, PCI DSS v4.0, HIPAA Security Rule.
- Crosswalk validation tooling (UI for reviewing STRM mappings, surfacing conflicts).
- Coverage-strength visualization across frameworks.
- Connector roster grows to ~25–30 (add Azure, GCP, Slack, Datadog, PagerDuty, Rippling/HRIS, Jamf/Intune, Cloudflare, GitLab, Snyk/Dependabot, Crowdstrike/SentinelOne).
- Vendor TPRM workflow expansion (questionnaire issuance, evidence reuse from vendor's own trust center where machine-readable).
- Policy redline / version diff (Notion-style, not Word track-changes).
- AI-assisted *gap explanation* and *evidence summarization* (still no auto-generated policy text or audit responses).
- Security-questionnaire response engine — answer customer questionnaires from existing evidence with one-click human approval per answer.

## 10.3 Phase 3 — "Audit ecosystem and scale"

- Trust center (public-facing posture page) — ship after enough customers ask, not before.
- Auditor partner program with credentialed auditor accounts.
- OSCAL ingest from external regulator catalogs (FedRAMP, CMMC).
- ClickHouse evidence-analytics path live.
- GDPR-specific privacy module (DPIA workflow, ROPA register, subject-request tracking).
- HIPAA-specific covered-entity workflow primitives.
- PCI-specific SAQ workflow.
- Plugin marketplace + per-tenant plugin install.
- Vendor-review portability — share/import vendor reviews across portfolio orgs that opt-in, breaking the per-customer vendor questionnaire repeat-loop.
- Multi-org / portfolio view — for security leaders advising several startups simultaneously.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 9. Tech Stack](./09-tech-stack.md) · **Next:** [11. Open Questions →](./11-open-questions.md)
