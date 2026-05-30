# 329 — Compliance meta-audit report (voltagent-qa-sec:compliance-auditor)

**Date:** 2026-05-28
**Auditor agent:** voltagent-qa-sec:compliance-auditor (loaded as Engineer context)
**Audit type:** Read-only-with-findings; JUDGMENT slice (no code changes)
**Audit scope:** v1-complete `main` at HEAD `bb9d0adc` (post-slice-371 reconcile)
**Audit posture:** operator-side (the platform as a SaaS service), NOT customer-side

---

## Executive summary

This is the "diligence the diligence tool" audit — does the security-atlas project
itself satisfy the compliance standards it serves to customers? A third-party
reviewer assessing security-atlas would not just check whether the product
implements compliance features; they would check whether the project can
demonstrate compliance to the same standards. This audit is a **gap analysis,
not a certification claim** — security-atlas is not SOC 2 certified, not
ISO 27001 certified, not HIPAA-attested. The question is whether the project
could pass those audits with the evidence it has today.

**Headline finding.** The platform's **technical** controls are exceptionally
strong — at or above the bar a typical Type II audit would require. The gaps are
**organizational and operational**: the project lacks the formal policy
artifacts (Incident Response plan, Business Continuity plan, Access Review
cadence, Data Retention policy, Asset Inventory) that a third-party auditor
expects to see laid out before the technical control evidence is even
examined. This pattern is normal for an early-stage OSS project; it is also
the load-bearing blocker for the v1 binary success criterion ("survive a
third-party security review of multi-tenant isolation in self-host
deployments").

The 5 High-severity gaps below collectively close the "no organizational
control evidence" structural deficit. Filing them as tracer-bullet slices
takes the project from "strong technical controls but no auditor-ready
artifacts" to "demonstrably compliance-ready as a sole-maintainer OSS
project."

| Severity      | Count | Notes                                                                                                                        |
| ------------- | ----- | ---------------------------------------------------------------------------------------------------------------------------- |
| Critical      | 0     | No control absences that would block a third-party from starting diligence                                                   |
| High          | 5     | No IR plan; no BCP/DR plan; no Access Review cadence; no Data Retention policy; no Asset Inventory                           |
| Medium        | 6     | No project-level risk register; no SLO targets; no vendor mgmt; no personnel policy; key mgmt policy gap; change mgmt policy |
| Low           | 4     | CHANGELOG audit-trail formalization; CoC reporting placeholder; SECURITY ack list missing; observability self-monitoring gap |
| Informational | 8     | Strong technical baselines + correctly-deferred items (GDPR Art 33, privacy v0, HIPAA per-customer-scope, PCI DSS N/A)       |

**Spillover slices filed:** 5 — slots 372, 373, 374, 375, 376.

- 372 — H-1 Incident Response plan (SOC 2 CC9 / ISO 27001 5.24-26 / NIST CSF Respond)
- 373 — H-2 Business Continuity / Disaster Recovery plan (SOC 2 A1 / ISO 27001 5.29-30 / NIST CSF Recover)
- 374 — H-3 GitHub org access review cadence (SOC 2 CC6.2/6.3 / ISO 27001 5.18+8.2)
- 375 — H-4 Data retention + disposal policy (SOC 2 C1.2 / ISO 27001 8.10 / GDPR Art 5(1)(e))
- 376 — H-5 Asset inventory document (SOC 2 CC3.2 / ISO 27001 5.9 / NIST CSF ID.AM)

Medium findings stay **audit-report-only** per JUDGMENT D3 — they share the
structural cause "no compliance evidence index", which the 5 Highs collectively
close. Low findings are documented for maintainer triage without follow-up
slices.

**Cross-reference to slices 327 + 328.** Zero findings duplicate the prior
audits. Slice 327's M-1 (JWT key rotation, filed as 366) IS the cryptographic-
key-rotation gap that ISO 27001 8.24 / NIST CSF PR.DS-7 expect — referenced
here as already-tracked; not re-filed. Slice 327's M-3 (cosign migration,
filed as 368) addresses ISO 27001 8.31 / NIST CSF PR.DS-6 software-supply-
chain-integrity — also referenced; not re-filed. Slice 328 findings are
code-quality concerns that don't map to compliance gaps.

---

## Compliance posture summary (load-bearing — the third-party-diligence artifact)

This is the table a third-party reviewer would expect to see at the top of any
auditor-ready report. Read this and you know where the project stands.

| Framework                           | Readiness verdict                       | Confidence | What's in place                                                                                     | What's missing                                                                                 |
| ----------------------------------- | --------------------------------------- | ---------- | --------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| **SOC 2 Type II (Security)**        | NO — pre-readiness                      | High       | Technical CC5 + CC6 + CC7 + CC8 strong; CC1 + CC2 governance solid                                  | CC3 risk-assessment formality, CC4 monitoring SLOs, CC9 incident response — all Highs          |
| **SOC 2 Type II (Availability)**    | NO — pre-readiness                      | High       | OTel observability instrumented; CI checks comprehensive                                            | No documented BCP/DR (High H-2); no project SLO targets (Medium)                               |
| **SOC 2 Type II (Confidentiality)** | NO — pre-readiness                      | Medium     | RLS enforcement at DB layer (invariant #6); evidence ledger integrity                               | No data retention policy (High H-4); no formal classification scheme                           |
| **ISO 27001:2022**                  | NO — pre-readiness                      | High       | Annex A 8.x technological controls largely present; 6.x people N/A (solo maintainer)                | Annex A 5.x organizational controls (5.9 asset inv, 5.18 access rights, 5.24 IR — all H)       |
| **NIST CSF 2.0**                    | PARTIAL — Identify+Respond+Recover gaps | High       | Protect (PR.\*) and Detect (DE.\*) exceptional via auth + CodeQL + Dependabot + Trivy               | Govern (GV.RM-3), Identify (ID.AM-1, ID.RA-5), Respond (RS.\*), Recover (RC.\*)                |
| **GDPR (Articles 25, 30, 32)**      | PARTIAL — Art 32 only                   | Medium     | Art 32 encryption/access controls excellent; Art 25 privacy module foundation (slice 180) present   | Art 30 ROPA + Art 33 breach notification DEFERRED per OQ #10 (correct deferral; not a Finding) |
| **HIPAA Security Rule**             | N/A operator-side                       | High       | 164.312 technical safeguards strong (auth, audit logs, integrity, transmission)                     | 164.308 admin safeguards Highs apply (H-1 IR, H-2 contingency plan); 164.310 N/A SaaS          |
| **PCI DSS v4.0**                    | N/A out of scope                        | High       | Platform does not process cardholder data — no CDE. Req 6/8/10/11/12 satisfied via SOC 2 CC overlap | Reqs 1-5, 7, 9 — N/A (no CDE)                                                                  |

**v1 binary success criterion projection.** The v1 test asks whether the
target persona ("solo security leader at a 50-150-person security-product
startup") could run their next SOC 2 audit out of security-atlas. The
**customer-side** answer is yes — the platform's UCF + evidence ledger +
audit-period freezing + OSCAL export are exceptional. The **operator-side**
answer is no, today — a customer doing security questionnaires against the
platform itself would find no SOC 2 attestation, no ISO 27001 cert, no
ROPA, no DPIA. Closing the 5 Highs below moves the platform from "can't
demonstrate operator-side compliance" to "documented operator-side
compliance program suitable for a sole-maintainer OSS project at this
stage."

---

## Methodology

The audit visited the surfaces required by the slice doc work-order: SOC 2
first (highest-leverage for v1 success), then GDPR, ISO 27001, HIPAA, PCI.
Per slice doc P0-329-1, the audit operates as if security-atlas were under
diligence as an operator — not as a customer using its own product. Per
P0-329-2, gap descriptions stay at "what's missing" level; no exploit-roadmap
detail is captured here. Per P0-329-6, the SCF redistribution-license
question is flagged but not resolved (it's a maintainer call outside this
audit's authority).

Severity rubric (operator-posture; documented in
`docs/audit-log/329-compliance-meta-audit-decisions.md` D2):

- **Critical** — control absent AND a third-party would refuse to start diligence
- **High** — control absent AND a third-party would flag as a Type II gap
- **Medium** — control present but evidence not discoverable in <30 min
- **Low** — control present, evidence discoverable, hardening recommended
- **Informational** — control N/A, correctly deferred, or strong baseline worth recording

Coverage attestation: each framework dimension below was visited; per-control
mapping uses the SCF anchors from slice 006's importer as the canonical
control catalog, per canvas invariant #7.

---

## SOC 2 Trust Services Criteria

### CC1 — Control Environment

| Sub-criterion                                       | Status     | Evidence                                                                                                   |
| --------------------------------------------------- | ---------- | ---------------------------------------------------------------------------------------------------------- |
| CC1.1 demonstrates commitment to integrity & ethics | PASS       | `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1); `CONTRIBUTING.md` DCO sign-off discipline                 |
| CC1.2 exercises board oversight                     | N/A (solo) | `GOVERNANCE.md` documents maintainer-led posture + advisory-council trigger (≥3 outside contributors ≥6mo) |
| CC1.3 establishes org structure                     | PASS       | `GOVERNANCE.md` (filed slice 181 per OQ #5)                                                                |
| CC1.4 demonstrates commitment to competence         | PASS       | `CONTRIBUTING.md` prerequisite list; AI-assist boundary documented in `CLAUDE.md`                          |
| CC1.5 enforces accountability                       | PASS       | DCO requirement + branch protection ruleset at `.github/branch-protection.json`                            |

**Finding:** none. CC1 is well-supported for a sole-maintainer OSS project.

### CC2 — Communication and Information

| Sub-criterion                            | Status  | Evidence                                                                                                           |
| ---------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------ |
| CC2.1 obtains/uses relevant info         | PASS    | Slice process (`docs/issues/*.md`) is the audit trail; decisions logs (`docs/audit-log/*.md`) per major decision   |
| CC2.2 communicates internally            | PASS    | DCO sign-off + `CHANGELOG.md` per-release narrative; `docs/issues/_STATUS.md` aggregated status                    |
| CC2.3 communicates with external parties | PARTIAL | `SECURITY.md` vuln reporting; `README.md` user-facing; no public roadmap document (intentional per AC-7 slice 050) |

**Finding:** none rising to High. The slice-process audit trail is **exceptional**
— a third-party auditor reading `docs/issues/_STATUS.md` and `CHANGELOG.md`
can reconstruct every change to the platform with the originating slice doc,
decisions log, PR, and merge commit. This pattern is stronger than what most
Series-A startups have in a JIRA + Notion + Slack ecosystem.

### CC3 — Risk Assessment

| Sub-criterion                       | Status         | Evidence                                                                                  |
| ----------------------------------- | -------------- | ----------------------------------------------------------------------------------------- |
| CC3.1 specifies suitable objectives | PASS           | `CLAUDE.md` constitutional principles; `Plans/canvas/01-vision.md` mission                |
| CC3.2 identifies & analyzes risk    | **FAIL — H-5** | No project-level asset inventory document; no formal risk register for the project itself |
| CC3.3 considers potential for fraud | N/A (solo)     | -                                                                                         |
| CC3.4 identifies & analyzes change  | PASS           | Slice process explicitly surfaces architectural change via canvas + ADRs                  |

**Finding H-5 (HIGH) — No project-level asset inventory.**

- **Frameworks:** SOC 2 CC3.2, ISO 27001 5.9, NIST CSF ID.AM
- **What's missing:** A document at the repo root or `docs/architecture/` that
  enumerates the project's assets — GitHub org, repo, container registry
  (`ghcr.io/mgoodric/security-atlas`), docs site (GitHub Pages), CI runners,
  release pipelines, third-party services (Codecov, GitGuardian), maintainer
  workstation, secrets-handling boundaries, who owns what.
- **What's there today:** scattered references in `docs/SELF_HOSTING.md`,
  `deploy/docker/docker-compose.yml`, `docs/RELEASE_READINESS.md`, ADR-0005
  (PAT scope). No consolidated inventory.
- **Why it matters:** the first questionnaire question is "list all assets in
  scope." Without an asset inventory, an auditor cannot scope an audit at all.
- **Recommendation:** file slice 376 — `docs/governance/asset-inventory.md`
  with a single Markdown table of assets + owner + classification + criticality.
- **Spillover slice:** 376

### CC4 — Monitoring Activities

| Sub-criterion                              | Status  | Evidence                                                                                                           |
| ------------------------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------ |
| CC4.1 selects ongoing/separate evaluations | PARTIAL | `internal/observability/otel/` OTel SDK instrumented; flake budget at `docs/flake-budget.md` + dashboard slice 352 |
| CC4.2 communicates deficiencies            | PASS    | CI surfaces govulncheck, CodeQL, Dependabot alerts directly to maintainer                                          |

**Finding M-1 (MEDIUM) — No project-level SLO targets / monitoring runbooks.**

- **Frameworks:** SOC 2 CC4.1, ISO 27001 8.16, NIST CSF DE.CM-1
- **What's missing:** the project does not have its own published SLO for the
  docs site, container registry availability, or release pipeline. The
  platform-level OTel instrumentation is excellent for **customer** deployments;
  no analogous monitoring exists for the project's own properties.
- **Disposition:** Bundled into the audit-report-only Mediums per D3. A future
  slice could file an `docs/governance/operational-slos.md` document; not
  blocking v1 binary criterion.

### CC5 — Control Activities

| Sub-criterion                                 | Status          | Evidence                                                                                                        |
| --------------------------------------------- | --------------- | --------------------------------------------------------------------------------------------------------------- |
| CC5.1 selects & develops control activities   | **EXCEPTIONAL** | Canvas invariants #1-#10 are the documented technical control selection                                         |
| CC5.2 selects & develops general tech ctls    | **EXCEPTIONAL** | Branch protection ruleset + 14 required CI checks; pre-commit hooks; CodeQL + govulncheck + Trivy + GitGuardian |
| CC5.3 deploys control activities via policies | PASS            | `CONTRIBUTING.md` documents the policy; `CLAUDE.md` is the constitutional layer                                 |

**Finding:** none. CC5 is the strongest CC in the audit. The technical control
surface (per slice 327's verified-positive observations §1-16) is at or above
the bar of a Type II audit at a Series-A SaaS company.

### CC6 — Logical and Physical Access

| Sub-criterion                                       | Status         | Evidence                                                                                                               |
| --------------------------------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------------- |
| CC6.1 logical access security software & infra      | PASS           | OIDC RP at `internal/auth/oidc/oidc.go` (slice 198); OAuth AS at `internal/api/oauth/*` (slices 187-192)               |
| CC6.2 registers/authorizes new users                | **FAIL — H-3** | No documented GitHub-org access review cadence; no quarterly review process                                            |
| CC6.3 removes access when no longer required        | **FAIL — H-3** | Same — no documented review-and-revoke process                                                                         |
| CC6.4 restricts physical access                     | N/A (SaaS)     | -                                                                                                                      |
| CC6.5 discontinues access when needed               | **FAIL — H-3** | Same                                                                                                                   |
| CC6.6 implements logical security measures          | PASS           | RLS invariant #6 enforced at DB layer; JWT auth substrate locked at slice 192                                          |
| CC6.7 protects info at rest, in transit, & disposal | PARTIAL        | TLS at deployment per `cmd/atlas/main.go:609` operator note; at-rest is operator-handled; **no disposal policy (H-4)** |
| CC6.8 monitors logical access                       | PARTIAL        | `migrations/sql/20260517000000_unified_audit_log.sql` records platform audit events; no project-level access audit     |

**Finding H-3 (HIGH) — No documented access review cadence for the GitHub organization.**

- **Frameworks:** SOC 2 CC6.2 + CC6.3 + CC6.5, ISO 27001 5.18 + 8.2, NIST CSF PR.AC-1
- **What's missing:** a documented cadence (quarterly / annual) for the
  maintainer to review who has access to the GitHub org, repo collaborators,
  CI secrets, third-party service connections (Codecov, GitGuardian,
  ghcr.io PATs), and to revoke unused access.
- **What's there today:** ADR-0005 (`docs/adr/0005-branch-protection-pat-vs-app.md`)
  documents the BRANCH_PROTECTION_READ_TOKEN PAT scope; `.github/branch-protection.json`
  is the source-of-truth ruleset. No periodic review mechanism.
- **Why it matters:** "do you review access at least annually?" is on every
  SOC 2 questionnaire. Auditors expect to see either evidence of past reviews
  OR a documented schedule that hasn't yet had its first cycle.
- **Recommendation:** file slice 374 — `docs/governance/access-review-cadence.md`
  with quarterly checklist + first scheduled review date.
- **Spillover slice:** 374

**Finding H-4 (HIGH) — No data retention + disposal policy.**

- **Frameworks:** SOC 2 CC6.7 + C1.2, ISO 27001 8.10, GDPR Art 5(1)(e)
- **What's missing:** the project does not document how long it retains
  artifacts (build artifacts in ghcr.io, CI logs in GitHub Actions, releases,
  audit-log decisions docs, MEMORY.md persistent state, etc.) or how disposal
  happens.
- **What's there today:** scattered references — `deploy/docker/test-self-host-bundle.sh:160`
  (backup suffix deletion); `migrations/sql/20260519000000_audit_periods_vendors_export.down.sql:11`
  (audit-meta retention policy comment); `migrations/sql/20260521010000_tenants_rename.sql:195`
  (tenant removal retention-semantics deferral). All inline comments, no policy.
- **Why it matters:** the platform itself is a GRC product — operators
  comparing it to Vanta will ask "what's YOUR retention policy on YOUR build
  artifacts?" before trusting their evidence inside it.
- **Recommendation:** file slice 375 — `docs/governance/data-retention-policy.md`
  with one table per data class + retention period + disposal method.
- **Spillover slice:** 375

### CC7 — System Operations

| Sub-criterion                               | Status         | Evidence                                                                                                               |
| ------------------------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------------- |
| CC7.1 detects / classifies system events    | PASS           | `internal/observability/otel/` instrumented across services; CI pipelines + StepSecurity Harden-Runner audit-mode hook |
| CC7.2 monitors anomalies                    | PARTIAL        | Platform-side strong; project-side gap (M-1)                                                                           |
| CC7.3 evaluates security events / incidents | **FAIL — H-1** | No documented incident response plan                                                                                   |
| CC7.4 responds to identified incidents      | **FAIL — H-1** | Same                                                                                                                   |
| CC7.5 recovers from identified incidents    | **FAIL — H-2** | No documented disaster recovery plan                                                                                   |

**Finding H-1 (HIGH) — No documented Incident Response plan.**

- **Frameworks:** SOC 2 CC7.3 + CC7.4 + CC9.1, ISO 27001 5.24 + 5.25 + 5.26, NIST CSF Respond (RS.\*), HIPAA 164.308(a)(6) security incident procedures
- **What's missing:** there is no `docs/governance/incident-response.md`
  (or runbook equivalent) that documents the severity rubric, roles,
  containment-eradication-recovery procedures, communications playbook, or
  post-incident review process for security or operational incidents
  affecting the project itself.
- **What's there today:** `SECURITY.md` documents the **inbound** vuln reporting
  process (5 business days ack, 10 business days assessment, 30 days
  high/critical fix target). That covers the report-receipt side, not the
  incident-response side once a real incident is in flight.
- **Why it matters:** this is the highest-load-bearing gap for v1 binary
  criterion. A third-party reviewer asking "what's your IR plan?" cannot be
  pointed at SECURITY.md alone — that's a coordinated-disclosure policy, not
  an IR plan. ISO 27001 5.24 is one of the clauses every certification audit
  spends time on.
- **Recommendation:** file slice 372 — `docs/governance/incident-response.md`
  with: severity rubric, sole-maintainer role-stacking acknowledgment, P0/P1/P2
  containment timelines, post-incident review template, comms playbook.
- **Spillover slice:** 372 (THE most-load-bearing finding for v1 binary criterion)

**Finding H-2 (HIGH) — No documented Business Continuity / Disaster Recovery plan.**

- **Frameworks:** SOC 2 CC7.5 + A1.2 + A1.3, ISO 27001 5.29 + 5.30 + 8.13 + 8.14, NIST CSF RC.\*, HIPAA 164.308(a)(7) contingency plan
- **What's missing:** there is no `docs/governance/business-continuity.md`
  (or similar) that documents RTO/RPO for the project's own properties (docs
  site, container registry, release pipeline), what happens if the maintainer
  is unavailable (bus-factor / succession — `GOVERNANCE.md` references this but
  doesn't operationalize it), or how the repo would be restored from a
  catastrophic GitHub-side loss.
- **What's there today:** `docs/SELF_HOSTING.md` documents operator-side
  backup (pg_dump nightly to S3) — that's customer guidance, not project DR.
  `GOVERNANCE.md` mentions a bus-factor / succession plan but doesn't
  operationalize it.
- **Why it matters:** SOC 2 Availability TSC is unauditable without an RTO/RPO
  statement. ISO 27001 5.29-30 require continuity arrangements.
- **Recommendation:** file slice 373 — `docs/governance/business-continuity.md`
  with: RTO/RPO targets per asset, GitHub-loss recovery procedure, maintainer-
  unavailable handoff procedure pointing at GOVERNANCE.md succession plan.
- **Spillover slice:** 373

### CC8 — Change Management

| Sub-criterion                                                                                     | Status          | Evidence                                                                    |
| ------------------------------------------------------------------------------------------------- | --------------- | --------------------------------------------------------------------------- |
| CC8.1 authorizes, designs, develops, configures, documents, tests, approves, & implements changes | **EXCEPTIONAL** | Slice process + PR + 14 required CI checks + branch protection + DCO + ADRs |

**Finding:** none. The change management surface is the second-strongest
control area in the audit (after CC5). The slice-doc-decisions-log-CHANGELOG
loop is a documented change record that exceeds what most SaaS startups
maintain. A formal Change Management Policy document would consolidate this
into one auditable artifact (Medium M-2 below).

### CC9 — Risk Mitigation

| Sub-criterion                                                       | Status              | Evidence                                                                                                            |
| ------------------------------------------------------------------- | ------------------- | ------------------------------------------------------------------------------------------------------------------- |
| CC9.1 identifies, selects, develops mitigation activities for risks | **FAIL — H-1, H-2** | No IR plan (H-1), no BCP/DR plan (H-2). These are CC9 too.                                                          |
| CC9.2 vendor management                                             | **PARTIAL — M-3**   | Third-party vendor relationships scattered (Codecov, GitGuardian, ghcr.io, mkdocs, dependabot); no consolidated VRM |

**Finding M-3 (MEDIUM) — No vendor management / third-party risk register.**

- **Frameworks:** SOC 2 CC9.2, ISO 27001 5.19 + 5.20, NIST CSF ID.SC-\*
- **What's missing:** no document enumerating third-party services the project
  depends on (Codecov, GitGuardian, ghcr.io, GitHub Actions runners, dependabot,
  Slack/email reporting addresses if any) with their risk grade.
- **Disposition:** Bundled audit-report-only per D3 — H-5 (Asset inventory)
  will likely consume this finding as part of the consolidated asset list.

### Availability + Confidentiality

| TSC                                 | Verdict        | Evidence + Gaps                                                                                              |
| ----------------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------ |
| Availability A1.1 capacity          | PARTIAL        | Customer-side perf testing exists; project-side capacity targets missing (M-1)                               |
| Availability A1.2 BCP               | **FAIL — H-2** | See H-2                                                                                                      |
| Availability A1.3 DR                | **FAIL — H-2** | See H-2                                                                                                      |
| Confidentiality C1.1 classification | PARTIAL        | Implicit classification via canvas (public docs, source code public; secrets via env vars); no formal scheme |
| Confidentiality C1.2 disposal       | **FAIL — H-4** | See H-4                                                                                                      |

---

## ISO 27001:2022 Annex A

The Annex A control set is the closest analog to SOC 2 CC. Each control below
is mapped to the canonical SCF anchor where applicable; SCF coverage is
verified via slice 006's importer at `internal/api/anchors/`.

### 5. Organizational controls (37 controls)

| Control                                                  | Status              | Notes                                                                                                   |
| -------------------------------------------------------- | ------------------- | ------------------------------------------------------------------------------------------------------- |
| 5.1 Policies for information security                    | PARTIAL             | `CLAUDE.md` + `CONTRIBUTING.md` + `SECURITY.md` together; no consolidated InfoSec policy                |
| 5.2 Information security roles & responsibilities        | PASS                | `GOVERNANCE.md` — maintainer-led; advisory council trigger at ≥3 contributors                           |
| 5.7 Threat intelligence                                  | PARTIAL             | Dependabot + GitGuardian + CodeQL provide reactive intel; no proactive threat-modeling cadence document |
| 5.9 Inventory of information and other associated assets | **FAIL — H-5**      | See SOC 2 CC3.2                                                                                         |
| 5.10 Acceptable use of assets                            | PASS                | `CODE_OF_CONDUCT.md`                                                                                    |
| 5.15-17 Access control / identity management             | PASS                | `internal/auth/users/users.go`; `.github/branch-protection.json`                                        |
| 5.18 Access rights                                       | **FAIL — H-3**      | See SOC 2 CC6.2                                                                                         |
| 5.19-20 Vendor / supplier security                       | PARTIAL (M-3)       | See SOC 2 CC9.2                                                                                         |
| 5.23 Information security for use of cloud services      | PARTIAL             | `docs/SELF_HOSTING.md` operator guidance; no project-side cloud-use inventory                           |
| 5.24-26 Information security incident management         | **FAIL — H-1**      | See SOC 2 CC7.3                                                                                         |
| 5.27 Learning from information security incidents        | PASS (structurally) | Slice process + audit-log decisions docs are the institutional memory                                   |
| 5.29-30 ICT readiness for business continuity            | **FAIL — H-2**      | See SOC 2 CC7.5                                                                                         |
| 5.34 Privacy and protection of PII                       | PARTIAL             | Privacy module foundation (slice 180); no operator-side PII handling document                           |

### 6. People controls (8 controls)

Largely **N/A** at the sole-maintainer stage; revisit when GOVERNANCE.md
advisory council trigger fires (≥3 outside contributors ≥6mo each). Filing
"personnel security policy" stubs now would be premature per canvas
anti-pattern (over-engineering for problems we don't have).

| Control                                                  | Status     | Notes                                                                                       |
| -------------------------------------------------------- | ---------- | ------------------------------------------------------------------------------------------- |
| 6.1 Screening                                            | N/A (solo) | -                                                                                           |
| 6.2 Terms and conditions of employment                   | N/A (solo) | -                                                                                           |
| 6.3 Information security awareness, education & training | M-4        | No formal awareness program; documented as contributor expectation in `CONTRIBUTING.md`     |
| 6.4 Disciplinary process                                 | PASS       | `CODE_OF_CONDUCT.md` Section 4 (Enforcement Guidelines: warning → temp ban → permanent ban) |
| 6.7 Remote working                                       | N/A (solo) | -                                                                                           |

**Finding M-4 (MEDIUM) — Personnel security stub deferred.**

- **Disposition:** Bundled audit-report-only per D3. Re-evaluate when
  contributor-count trigger from `GOVERNANCE.md` fires.

### 7. Physical controls (14 controls)

**Largely N/A** for SaaS-only operator. Physical security of operator-hosted
deployments is the operator's concern (documented in `docs/SELF_HOSTING.md`).

### 8. Technological controls (34 controls)

This is the strongest section of the audit.

| Control                                                     | Status              | Evidence                                                                                                       |
| ----------------------------------------------------------- | ------------------- | -------------------------------------------------------------------------------------------------------------- |
| 8.1 User endpoint devices                                   | PARTIAL             | No documented endpoint policy for the maintainer's workstation; informally covered                             |
| 8.2 Privileged access rights                                | **PASS / FAIL H-3** | Strong technical impl; missing review cadence — see H-3                                                        |
| 8.3 Information access restriction                          | PASS                | RLS at DB layer (invariant #6); JWT current_tenant claim per slice 192                                         |
| 8.5 Secure authentication                                   | PASS                | OIDC RP + OAuth AS substrate per slices 187-198                                                                |
| 8.7 Protection against malware                              | PARTIAL             | CodeQL + Trivy + govulncheck; no documented malware-protection policy                                          |
| 8.8 Management of technical vulnerabilities                 | PASS                | Dependabot weekly + CodeQL weekly + Trivy + govulncheck — all wired in CI                                      |
| 8.9 Configuration management                                | PASS                | `.github/branch-protection.json` + `.github/dependabot.yml` + `.pre-commit-config.yaml` are config-as-code     |
| 8.10 Information deletion                                   | **FAIL — H-4**      | See SOC 2 C1.2                                                                                                 |
| 8.11 Data masking                                           | PARTIAL             | Demo seed dataset (slice 205); no formal data-masking pattern                                                  |
| 8.12 Data leakage prevention                                | PASS                | GitGuardian wired in branch protection; slice 367 5xx-error-detail-leakage closed                              |
| 8.13 Information backup                                     | **FAIL — H-2**      | See SOC 2 CC7.5                                                                                                |
| 8.14 Redundancy of information processing facilities        | **FAIL — H-2**      | See H-2; no documented redundancy plan for the project's own properties                                        |
| 8.15 Logging                                                | PASS                | `migrations/sql/20260517000000_unified_audit_log.sql` + OTel instrumentation                                   |
| 8.16 Monitoring activities                                  | PARTIAL (M-1)       | See SOC 2 CC4.1                                                                                                |
| 8.18 Use of privileged utility programs                     | PASS                | `migrations/bootstrap/01-roles.sql` separation of `atlas_migrate` vs `atlas_app`                               |
| 8.19 Installation of software on operational systems        | PASS                | Container distroless base; multi-stage builds                                                                  |
| 8.21 Security of network services                           | PASS                | OAuth AS standards-based per ADR-0003; TLS at deployment edge per `cmd/atlas/main.go:609`                      |
| 8.22 Segregation of networks                                | OPERATOR-SIDE       | -                                                                                                              |
| 8.23 Web filtering                                          | N/A                 | -                                                                                                              |
| 8.24 Use of cryptography                                    | PARTIAL             | Argon2id RFC 9106 + ES256 JWT signing; **key rotation gap from slice 327 M-1 → 366** (cross-ref; not re-filed) |
| 8.25 Secure development life cycle                          | PASS                | Slice process + CC5 + CC8 strength                                                                             |
| 8.26 Application security requirements                      | PASS                | OWASP top-10 spot-check via slice 327; CodeQL                                                                  |
| 8.27 Secure system architecture and engineering             | PASS                | Canvas architecture invariants #1-#10                                                                          |
| 8.28 Secure coding                                          | PASS                | `.golangci.yml` strict mode; pre-commit hooks; PR review                                                       |
| 8.29 Security testing in development and acceptance         | PASS                | 4 enforced test surfaces (slice 069); ship-gate slice if needed                                                |
| 8.30 Outsourced development                                 | N/A                 | -                                                                                                              |
| 8.31 Separation of development, test, and production        | PARTIAL (M-5)       | docker-compose dev / staging / prod separated by deploy artifact; no documented promotion policy               |
| 8.32 Change management                                      | PASS                | See SOC 2 CC8.1                                                                                                |
| 8.33 Test information                                       | PASS                | Demo seed (slice 205) explicitly synthetic                                                                     |
| 8.34 Protection of information systems during audit testing | PASS                | Audit-period freezing per canvas §8.4; integration test fixtures synthetic                                     |

**Finding M-5 (MEDIUM) — No documented promotion/release policy.**

- **Frameworks:** ISO 27001 8.31, NIST CSF PR.IP-3
- **What's there today:** `.github/workflows/release.yml` + `release-please.yml`
  automate the mechanics; `docs/releases.md` describes the release model.
- **Disposition:** Bundled audit-report-only per D3.

---

## NIST CSF 2.0

NIST CSF 2.0 added Govern as a sixth function. The audit visits all six.

### Govern (GV)

| Category                                     | Status        | Evidence                                                                           |
| -------------------------------------------- | ------------- | ---------------------------------------------------------------------------------- |
| GV.OC Organizational Context                 | PASS          | `Plans/canvas/01-vision.md` mission + personas                                     |
| GV.RM Risk Management Strategy               | PARTIAL       | Implicit in canvas + slice process; no formal risk register (M-6 below)            |
| GV.RR Roles, Responsibilities, & Authorities | PASS          | `GOVERNANCE.md`                                                                    |
| GV.PO Policy                                 | PARTIAL       | Scattered across `CLAUDE.md` + `CONTRIBUTING.md` + `SECURITY.md`; not consolidated |
| GV.OV Oversight                              | N/A (solo)    | -                                                                                  |
| GV.SC Supply Chain Risk Management           | PARTIAL (M-3) | See SOC 2 CC9.2                                                                    |

**Finding M-6 (MEDIUM) — No formal risk register for the project.**

- **Frameworks:** NIST CSF GV.RM-3, ISO 27001 6.1.2, SOC 2 CC3.1 (loose match)
- **Disposition:** Bundled audit-report-only per D3. The slice-process risk
  surfacing (canvas + decisions logs) covers this informally. A formal
  `docs/governance/project-risks.md` could consolidate.

### Identify (ID)

| Category               | Status         | Evidence                                                                      |
| ---------------------- | -------------- | ----------------------------------------------------------------------------- |
| ID.AM Asset Management | **FAIL — H-5** | See SOC 2 CC3.2                                                               |
| ID.RA Risk Assessment  | PARTIAL        | Per-slice threat-model in slice doc template; no aggregated project risk view |
| ID.IM Improvement      | PASS           | `docs/audit-log/*` decisions logs + slice retrospectives                      |

### Protect (PR)

EXCEPTIONAL. CodeQL + Dependabot + Trivy + GitGuardian + govulncheck + Argon2id +
RLS-at-DB-layer + JWT-with-ES256 + StepSecurity Harden-Runner + branch
protection + pre-commit hooks. See slice 327's verified-positive observations
list. No findings.

### Detect (DE)

PARTIAL. Customer-side detection (audit log, evidence drift) excellent;
project-side detection (M-1 monitoring SLOs) partial.

### Respond (RS)

**FAIL — H-1.** No IR plan = no RS.MA (Management) or RS.AN (Analysis) or
RS.CO (Communications) or RS.MI (Mitigation) documentation.

### Recover (RC)

**FAIL — H-2.** No BCP/DR plan = no RC.RP (Recovery Plan) documentation.

---

## GDPR (Articles 25, 30, 32; Art 33 deferred per OQ #10)

Per slice doc P0-329-5, this audit does NOT file GDPR-specific spillovers for
the breach-notification workflow — OQ #10 defers that to phase 3. The
following is a gap inventory only.

| Article                                         | Status                                  | Evidence + Gaps                                                                                                                             |
| ----------------------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Art 5(1)(a) lawfulness/fairness/transparency    | N/A (no PII processed by project today) | Public-OSS project; project does not collect PII from users via the platform                                                                |
| Art 5(1)(e) storage limitation                  | **FAIL — H-4**                          | See SOC 2 C1.2; no retention/disposal policy                                                                                                |
| Art 25 Data protection by design and by default | PARTIAL                                 | Privacy module foundation slice 180 (`migrations/sql/20260520020000_audit_log_subject_module.sql`); pre-commitment only; v0 deferred to v2+ |
| Art 30 Records of processing activities (ROPA)  | DEFERRED                                | Privacy module v0 deferred per OQ #7 — ROPA is module-internal; not a Finding                                                               |
| Art 32 Security of processing                   | PASS                                    | Encryption (Argon2id + ES256), access control (RLS), pseudonymization (audit-log subject_module column)                                     |
| Art 33 Breach notification                      | DEFERRED                                | OQ #10 explicitly defers breach-notification workflow scope; not a Finding per slice doc P0-329-5                                           |

**Disposition:** GDPR Art 32 is **strong**. Art 25 has the foundation
(slice 180). Art 30 + Art 33 are correctly deferred. H-4 (data retention)
closes the Art 5(1)(e) gap. **No GDPR-specific spillover slices filed** per
P0-329-5; this section is gap inventory only.

---

## HIPAA Security Rule

Per slice doc: per-platform operator-side audit, not per-customer-scope. The
FrameworkScope intersection (canvas §5.5) is the per-tenant-scope mechanism;
this audit examines the operator's own posture for the technical and
administrative safeguards.

### Administrative safeguards (164.308)

| Standard                                       | Status              | Notes                                                                                                              |
| ---------------------------------------------- | ------------------- | ------------------------------------------------------------------------------------------------------------------ |
| 164.308(a)(1) Security mgmt process            | PARTIAL             | Canvas + slice process is the program; no formal risk-analysis cadence (M-6)                                       |
| 164.308(a)(2) Assigned security responsibility | PASS                | `GOVERNANCE.md` maintainer role                                                                                    |
| 164.308(a)(3) Workforce security               | N/A (solo)          | -                                                                                                                  |
| 164.308(a)(4) Information access mgmt          | PARTIAL — H-3       | See CC6.2                                                                                                          |
| 164.308(a)(5) Security awareness/training      | PARTIAL — M-4       | See ISO 27001 6.3                                                                                                  |
| 164.308(a)(6) Security incident procedures     | **FAIL — H-1**      | See SOC 2 CC7.3                                                                                                    |
| 164.308(a)(7) Contingency plan                 | **FAIL — H-2**      | See SOC 2 CC7.5                                                                                                    |
| 164.308(a)(8) Evaluation                       | PARTIAL             | This audit is one such evaluation; no cadence documented                                                           |
| 164.308(b)(1) BAA                              | N/A (operator-side) | BAA would be required only if the project processed ePHI for a covered entity; deferred to customer-FrameworkScope |

### Physical safeguards (164.310) — N/A SaaS, operator-handled

### Technical safeguards (164.312)

| Standard                                | Status | Evidence                                                                                |
| --------------------------------------- | ------ | --------------------------------------------------------------------------------------- |
| 164.312(a) Access control               | PASS   | OIDC + OAuth + RLS                                                                      |
| 164.312(b) Audit controls               | PASS   | `migrations/sql/20260517000000_unified_audit_log.sql`                                   |
| 164.312(c) Integrity                    | PASS   | Evidence ledger sha256 content-hash per record (canvas §9); append-only at policy layer |
| 164.312(d) Person/entity authentication | PASS   | OIDC + JWT current_tenant claim                                                         |
| 164.312(e) Transmission security        | PASS   | TLS at deployment edge; CORS exact-match only                                           |

**Disposition:** HIPAA Security Rule technical safeguards are **strong**.
Administrative gaps map cleanly to H-1, H-2, H-3 already filed. No HIPAA-
specific spillover slices filed (mapped to the SOC 2 spillovers).

---

## PCI DSS v4.0

**The platform does not process cardholder data.** No CDE exists. Most of
PCI is **N/A**. Operator-side controls overlap with SOC 2:

| Requirement                                                                        | Status                           | Notes                                                                           |
| ---------------------------------------------------------------------------------- | -------------------------------- | ------------------------------------------------------------------------------- |
| Req 1-5 (network, vendor defaults, CHD protection, encryption in transit, malware) | N/A                              | No CDE                                                                          |
| Req 6 Develop & maintain secure systems and applications                           | PASS                             | SOC 2 CC5 + CC8 strength carries this                                           |
| Req 7 Restrict access by business need-to-know                                     | N/A                              | No CHD                                                                          |
| Req 8 Identify users & authenticate access                                         | PASS                             | OAuth AS + OIDC                                                                 |
| Req 9 Restrict physical access                                                     | N/A                              | SaaS-only                                                                       |
| Req 10 Log & monitor all access                                                    | PASS                             | Unified audit log + OTel                                                        |
| Req 11 Test security of systems & networks regularly                               | PASS                             | CodeQL weekly + Trivy + govulncheck; no third-party pen test (Medium not filed) |
| Req 12 Support info-sec with org policies                                          | **PARTIAL — H-1, H-3, H-4, H-5** | Consolidated via the 5 Highs; req 12.10 IR plan = H-1                           |

**Disposition:** PCI DSS gaps map entirely to the 5 Highs and N/A items. No
PCI-specific spillover slices filed.

---

## Low-severity findings

### L-1 (LOW) — `CODE_OF_CONDUCT.md` reporting contact placeholder

- **File:** `CODE_OF_CONDUCT.md:3` (reportingPlaceholder = "[INSERT CONTACT METHOD]")
- **Framework:** ISO 27001 6.4 (Disciplinary process — needs a real contact)
- **Recommendation:** maintainer fills in the report-to contact (e.g.,
  conduct@security-atlas.dev or similar). Audit-report-only.

### L-2 (LOW) — `SECURITY-ACKNOWLEDGEMENTS.md` referenced but absent

- **File:** `SECURITY.md:78` ("A `SECURITY-ACKNOWLEDGEMENTS.md` file at the repo root will record...")
- **Framework:** ISO 27001 5.27 (Learning from incidents — recognition cadence)
- **Recommendation:** create empty `SECURITY-ACKNOWLEDGEMENTS.md` stub OR
  remove forward reference from SECURITY.md. Audit-report-only.

### L-3 (LOW) — No project-side observability self-monitoring

- **File:** `internal/observability/otel/` (instrumentation exists; no
  documented project-side observability for docs site / ghcr.io / releases)
- **Framework:** SOC 2 CC4.1 (overlaps M-1)
- **Recommendation:** could fold into H-1 IR plan as the "detect" half.
  Audit-report-only.

### L-4 (LOW) — CHANGELOG.md audit-trail formalization gap

- **File:** `CHANGELOG.md`
- **Framework:** SOC 2 CC2.2, ISO 27001 8.32
- **Recommendation:** the CHANGELOG already serves as an audit trail; a single
  paragraph at the top stating "this file is the canonical change record for
  SOC 2 CC8.1 / ISO 27001 8.32 evidence purposes" would make it discoverable.
  Audit-report-only.

---

## Informational findings

I-1 — Strong slice-process audit trail. (CC2.2 / 8.32; verified-positive)
I-2 — Strong branch-protection ruleset. (CC8.1; verified-positive)
I-3 — Strong CI scanner coverage (CodeQL + Trivy + govulncheck + Dependabot + GitGuardian + StepSecurity). (CC7 / 8.8; verified-positive)
I-4 — Strong DCO sign-off discipline. (CC1.5; verified-positive)
I-5 — GDPR Art 33 correctly deferred per OQ #10 — not a Finding.
I-6 — Privacy module v0 correctly deferred per OQ #7 — not a Finding.
I-7 — HIPAA per-customer-scope BAA correctly deferred to FrameworkScope.
I-8 — PCI DSS correctly N/A — platform doesn't process card data.

---

## Cross-reference to slices 327, 328 (no re-files)

| Compliance angle                           | Slice 327 finding                         | Slice 327 spillover | This audit                                        |
| ------------------------------------------ | ----------------------------------------- | ------------------- | ------------------------------------------------- |
| ISO 27001 8.24 cryptography (key rotation) | M-1 JWT signing key rotation              | 366                 | Reference only; **not re-filed**                  |
| ISO 27001 8.31 supply chain integrity      | M-3 OSCAL signing uses ed25519 not cosign | 368                 | Reference only; **not re-filed**                  |
| CC7.3 logging hygiene                      | M-2 verbose error reflection              | 367                 | Reference only; **not re-filed** (closed already) |
| CC6.1 authentication                       | H-1 OIDC ID-token nonce missing           | 365                 | Reference only; **not re-filed** (closed already) |

Slice 328 findings are code-quality consolidations (writeJSON dedup, web/lib/api.ts
split, auth clock injection). None map to compliance gaps; no overlap to manage.

---

## Most-load-bearing finding for v1 binary criterion

**H-1 — No documented Incident Response plan.**

The v1 binary test asks whether the target persona could survive a third-party
security review of multi-tenant isolation. "What is your incident response
plan?" is asked on every diligence call. SECURITY.md covers coordinated
disclosure (inbound vuln reports) — that's not an IR plan. Without H-1
closed, the project answers "we don't have one" in a sales-cycle moment that
costs the deal. The other 4 Highs are important; H-1 is the one that ends
the conversation if absent.

---

## Disposition

**Spillover slices filed:** 372, 373, 374, 375, 376 (5 of 5 cap; H-1 through H-5).

**Mediums kept audit-report-only:** M-1 (monitoring SLOs), M-2 (change-mgmt
policy consolidation), M-3 (vendor mgmt), M-4 (personnel security policy),
M-5 (promotion policy), M-6 (project risk register). Per D3, all 6 Mediums
share the structural cause "no consolidated compliance evidence index" — the
5 Highs collectively close that structural deficit at the policy-document
layer.

**Lows kept audit-report-only:** L-1 (CoC contact placeholder), L-2 (SECURITY
ack list missing), L-3 (project-side observability gap), L-4 (CHANGELOG
formalization).

**Informational:** 8 items recorded as positive baseline + correctly-deferred items.

---

## Companion documents

- **Decisions log:** `docs/audit-log/329-compliance-meta-audit-decisions.md` — JUDGMENT calls (scope, severity rubric, bundling, deferral handling).
- **Spillover slices:**
  - `docs/issues/372-incident-response-plan.md` (governance — H-1)
  - `docs/issues/373-business-continuity-plan.md` (governance — H-2)
  - `docs/issues/374-github-access-review-cadence.md` (governance — H-3)
  - `docs/issues/375-data-retention-disposal-policy.md` (governance — H-4)
  - `docs/issues/376-project-asset-inventory.md` (governance — H-5)
