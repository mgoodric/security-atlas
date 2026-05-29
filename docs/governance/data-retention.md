# Data retention and disposal policy

**Status:** Active governance document.
**Filed:** 2026-05-28 by slice 375.
**Closes:** Slice 329 compliance meta-audit finding **H-4** (no documented data retention and disposal policy).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual, co-scheduled with the [incident-response plan](./incident-response.md), [business-continuity plan](./business-continuity.md), and [access-review plan](./access-review.md) tabletop. Next review: 2027-05-28.

---

## Why this document exists

The project produces and stores artifacts about itself — CI logs, container
images, governance documents, audit reports, decisions logs, the
CHANGELOG. Until this document, no policy answered: how long does the
project keep each of these? How are they disposed of when retention
expires? Under what conditions is retention extended (legal hold)?

Slice 329's compliance meta-audit identified this gap as **H-4** — High
severity, load-bearing for SOC 2 C1.2 (Confidentiality — disposal of
confidential information), ISO 27001 8.10 (Information deletion), and
GDPR Article 5(1)(e) ("storage limitation" — personal data kept no
longer than necessary). All three expect a documented retention policy
with per-category durations and disposal procedures. The platform itself
is a GRC product; operators comparing it to commercial alternatives will
ask "what is **your** retention policy on **your** build artifacts?"
before trusting their evidence inside it.

Scattered references existed before this document — backup-suffix
deletion at [`deploy/docker/test-self-host-bundle.sh:160`](../../deploy/docker/test-self-host-bundle.sh),
the audit-meta retention-policy comment at
[`migrations/sql/20260519000000_audit_periods_vendors_export.down.sql:11`](../../migrations/sql/20260519000000_audit_periods_vendors_export.down.sql),
the tenant-removal retention-semantics deferral comment at
[`migrations/sql/20260521010000_tenants_rename.sql:195`](../../migrations/sql/20260521010000_tenants_rename.sql).
All inline comments; no consolidated policy. This document is the
consolidation.

This document **describes capabilities**, not certifications.
security-atlas is not SOC 2 certified, not ISO 27001 certified, not
HIPAA-attested. The retention durations and disposal procedures below
document what the project commits to today; they do not claim third-party
attestation of that commitment.

Cross-references:

- [`docs/governance/incident-response.md`](./incident-response.md) — §8 (Documentation and audit trail) names the incident-log retention shape this document operationalizes. Incident logs in `docs/incidents/` follow the same indefinite-retention posture as the rest of the governance corpus.
- [`docs/governance/business-continuity.md`](./business-continuity.md) — §5 (Backup strategy) names the backup-retention windows this document treats as policy-driven rather than ad-hoc; the BCP plan committed to 30-day Postgres-dump retention and 7-day Unraid offsite retention windows that are reproduced here so they live in one place.
- [`docs/governance/access-review.md`](./access-review.md) — per-review evidence artifacts at `docs/governance/access-reviews/` follow the same governance-doc indefinite-retention posture; access-review CHANGELOG bullets follow this document's CHANGELOG retention.
- [`GOVERNANCE.md`](../../GOVERNANCE.md) — the project's licensing posture (Apache 2.0 commitment per the bus-factor & succession clause) underwrites the "indefinite retention" commitment for the governance corpus: a permissively-licensed Markdown corpus on a public Git repository is durably retained by the contributor ecosystem, not just by the maintainer.
- [`SECURITY.md`](../../SECURITY.md) — disclosure-triggered evidence (PVR advisory threads, CVE advisories, security incident logs) follows this document's retention; the inbound disclosure intake process is unchanged.
- [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4.6.5 (AI-assist boundary) + §4.6.7 (Board-narrative AI-assist) — the platform-product immutability requirements for AI-assisted board-narrative drafts are constitutional; this document's project-self retention scope does not modify them, only cross-references them.
- [`Plans/canvas/05-scopes.md`](../../Plans/canvas/05-scopes.md) §5.4 — canvas invariant #6 (PostgreSQL Row-Level Security at the database layer) is the runtime tenant-isolation primitive that constrains what disposal looks like when an operator disposes of one tenant's data inside a running platform deployment.
- [`docs/audits/327-security-audit-security-auditor-report.md`](../audits/327-security-audit-security-auditor-report.md) — verified-positive controls (encryption at rest, no `InsecureSkipVerify` in TLS configuration, no logging of sensitive material) that complement this document's disposal procedures.
- [`docs/audits/329-compliance-meta-audit-report.md`](../audits/329-compliance-meta-audit-report.md) — the audit finding H-4 that filed this slice.
- [`.github/workflows/edge-image-prune.yml`](../../.github/workflows/edge-image-prune.yml) — slice 207's weekly GHCR cleanup workflow that operationalizes the edge-image rolling-window disposal documented in §3 below.

---

## 1. Purpose and scope

### What this policy covers

This policy is about retention and disposal of artifacts the
**security-atlas project itself** produces and stores. Specifically:

- **CI/CD artifacts** — GitHub Actions logs, workflow run history,
  artifact uploads, container images pushed to
  `ghcr.io/mgoodric/security-atlas`.
- **Source code and git history** — every commit, tag, branch, and
  release artifact on `mgoodric/security-atlas`.
- **Governance corpus** — documents in `docs/governance/`,
  architectural decision records in `docs/adr/`, the canvas under
  `Plans/`, the CHANGELOG, the README, and the repo-root community
  health files (`SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `GOVERNANCE.md`, `LICENSE`).
- **Audit-trail artifacts** — audit reports under `docs/audits/`,
  decision logs under `docs/audit-log/`, slice docs under
  `docs/issues/`, per-review access-review artifacts under
  `docs/governance/access-reviews/`, and incident logs under
  `docs/incidents/`.
- **Issue-tracker state** — GitHub Issues, Pull Requests, GitHub
  Discussions content, GitHub Security Advisories, and CVE entries
  filed through GitHub's CVE Numbering Authority workflow.
- **Maintainer-operated SaaS instance** — Postgres state, evidence
  object storage, observability stack data, and audit logs on the
  single-host Unraid deployment the maintainer runs for personal use
  (as defined by the [BCP plan §1](./business-continuity.md#1-purpose-and-scope)).
  Retention here is operator-side concerning the maintainer's
  own deployment; tenant-data inside that deployment follows the
  AI-assist boundary and canvas invariants below.
- **Third-party-service derived state** — Codecov coverage history,
  GitGuardian scan history, Dependabot alert history, CodeQL finding
  history, where the project consumes those services.
- **Backups** — every backup substrate documented in the [BCP plan §5](./business-continuity.md#5-backup-strategy)
  has a retention window; this document treats those windows as
  policy-bound rather than operationally ad-hoc.

### What this policy does not cover

- **Tenant data inside operator-hosted deployments.** When an operator
  self-hosts security-atlas and ingests evidence on behalf of their
  own customers, the lifecycle of that evidence is **governed by the
  contract between the operator and their customers**, plus the
  platform invariants (canvas invariant #3 append-only evidence
  ledger; canvas invariant #6 RLS tenant isolation). This policy is
  the project's policy for the project's own surfaces; it is **not**
  a template for operators to adopt verbatim.
- **GDPR Article 17 ("right to erasure") per-data-subject workflows.**
  The platform supports per-record deletion via tenant-controlled
  operations; the workflow shape is product surface, not governance
  policy, and is tracked through the privacy module (slice 180
  foundation; v0 deferred per OQ #7). This document references the
  Article 5(1)(e) storage-limitation principle that **does** apply to
  the project's own surfaces; it does not pretend GDPR compliance
  more broadly.
- **GDPR Article 33 breach-notification workflow.** Open Question #10
  in the canvas explicitly defers this to phase 3.
- **The platform-product's customer-facing audit-period freezing
  semantics.** Canvas §8.4's audit-period freezing is a product
  invariant; how an operator runs an audit period inside their
  deployment is not in scope for this document.
- **Maintainer's personal-IT retention.** The maintainer's workstation,
  password manager, email inbox, and other personal-IT surfaces are
  out of scope. They are referenced where they hold project-relevant
  material (e.g., the maintainer-local Git mirror per BCP §5 Tier 0),
  but their day-to-day retention is the maintainer's personal-IT
  concern.

### What counts as "disposal"

Disposal under this policy means **any of the following**:

1. **Hard delete** — the artifact is permanently removed from
   storage. No copy remains under the project's control. Examples:
   stale CI secret deleted via `gh secret delete`; expired Postgres
   dump removed by the offsite lifecycle rule.
2. **Soft delete with tombstone** — the artifact's record persists
   for audit-trail purposes but is marked superseded or no-longer-
   active. Examples: a revoked PAT (GitHub retains the audit record
   that it existed and was revoked); a yanked release (the GitHub
   release is marked as "Pre-release" or removed but the tag remains
   per BCP §6 Scenario D).
3. **Cryptographic erasure** — the artifact is encrypted at rest and
   the key is destroyed, rendering the ciphertext unrecoverable. Used
   where hard deletion of the underlying medium is infeasible
   (typically an offsite backup where the storage tier is
   maintainer-controlled but selective deletion is impractical).
4. **Aging out via rotation** — the artifact exists in a
   fixed-capacity rolling window that overwrites older entries as
   new ones arrive. Examples: GitHub Actions log retention (90-day
   default); Postgres-dump 30-day rolling offsite retention per
   BCP §5 Tier 3; container-edge-tag image-prune workflow
   ([`.github/workflows/edge-image-prune.yml`](../../.github/workflows/edge-image-prune.yml)).
5. **Ledger tombstone (append-only-with-supersede)** — the artifact
   cannot be deleted because it is part of an append-only ledger
   (canvas invariant #3); a tombstone record is appended that marks
   the original as superseded, lost, or artifact-lost (per BCP §6
   Scenario C). This is the disposal posture for the evidence
   ledger and the unified audit log.

The disposal method per data category is named explicitly in §3.

---

## 2. Data inventory and categories

The project's data inventory groups into seven categories for
retention purposes. The categories are calibrated so that within each
category, retention duration and disposal method are uniform; across
categories, the retention floor is set by the most-stringent framework
that touches that category.

### 2.1 Source code and git history

**Examples.** Every commit, every tag, every branch on
`mgoodric/security-atlas`; the release-tag set; the maintainer-local
Git mirror (per BCP §5 Tier 0); any contributor's clone of the repo.

**Sensitivity.** Public. The project is open-source under the
license documented at [`LICENSE`](../../LICENSE).

**Where it lives.** Primary: GitHub (`mgoodric/security-atlas`).
Secondary: maintainer-local mirror; every contributor's clone.

### 2.2 Governance corpus

**Examples.** This document; every other document under
`docs/governance/`; every ADR under `docs/adr/`; the canvas under
`Plans/`; `CHANGELOG.md`; `README.md`; the repo-root community-health
files (`SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
`GOVERNANCE.md`, `LICENSE`); the slice docs under `docs/issues/`.

**Sensitivity.** Public. These are deliberate communications to
contributors, adopters, and third-party reviewers.

**Where it lives.** In repo; inherits Tier 0 (per BCP §2) retention
posture.

### 2.3 Audit-trail artifacts

**Examples.** Audit reports under `docs/audits/`; decision logs
under `docs/audit-log/`; per-review access-review artifacts under
`docs/governance/access-reviews/`; incident logs under
`docs/incidents/`; the unified audit log on the maintainer-operated
SaaS instance (per the
[`migrations/sql/20260517000000_unified_audit_log.sql`](../../migrations/sql/20260517000000_unified_audit_log.sql)
schema, slice 040).

**Sensitivity.** Public-by-default in the repo (per the [IR plan §8](./incident-response.md#confidentiality)
and [access-review plan §6](./access-review.md#confidentiality) confidentiality
posture); the unified audit log on the SaaS instance is operator-internal
(per canvas §5.4 RLS — only the operating tenant's rows are visible to
that tenant; cross-tenant access requires the `super_admin` flag per
slices 142 + 197).

**Where it lives.** In repo for the public artifacts; in Postgres
(`atlas_audit_log` table per slice 040) for the runtime audit log;
in offsite Postgres dumps per BCP §5 Tier 3 for the backup copy.

### 2.4 CI/CD artifacts

**Examples.** GitHub Actions workflow run logs; workflow artifact
uploads (test reports, coverage uploads, build artifacts); container
images pushed to `ghcr.io/mgoodric/security-atlas`; CI scanner finding
history (CodeQL, govulncheck, Trivy, GitGuardian, Dependabot,
StepSecurity Harden-Runner); release-please state.

**Sensitivity.** Public by virtue of running on a public repository.
Workflow logs may contain reference to in-repo file paths and CI
configuration; secrets are masked by GitHub's machinery and the
project's CI does not echo secret values.

**Where it lives.** GitHub Actions (workflow runs, artifacts); GitHub
Container Registry (`ghcr.io/mgoodric/security-atlas`); GitHub Security
tab (scanner findings); maintainer's local environment for any
re-pulled artifact.

### 2.5 Maintainer-operated SaaS instance state

**Examples.** PostgreSQL database state (tenant rows, evidence ledger
rows, audit log rows, control catalog rows); evidence object storage
(per-record artifacts on the S3-compatible bucket); observability
stack stateful data (Prometheus metrics, Tempo traces, Loki logs);
Docker volume state; OAuth Authorization Server signing keys per
[ADR-0003](../adr/0003-oauth-authorization-server.md).

**Sensitivity.** Operator-internal. The maintainer is the operator.
Tenant data inside the instance follows canvas invariant #6 (RLS at
the database layer); cross-tenant disposal does not affect other
tenants by design.

**Where it lives.** Single-host Unraid deployment (per BCP §1); offsite
backup per BCP §5 Tier 3.

### 2.6 Third-party-service state

**Examples.** Codecov coverage history (per-PR snapshots, per-branch
trends); GitGuardian scan history (per-commit scan results, alert
history); Dependabot alert history (open + closed alerts); CodeQL
finding history (security tab); GitHub-native audit log entries
visible at `https://github.com/mgoodric/security-atlas/security` and
`https://github.com/settings/security-log`.

**Sensitivity.** Visible to the maintainer and to anyone with
read access to the repository's security tab. Vendor-specific
visibility rules apply.

**Where it lives.** At the third-party vendor's infrastructure.
Recovery from vendor-side loss is per BCP §5 Tier 4 (vendor-
recovered; the project does not back these up independently).

### 2.7 Issue-tracker state

**Examples.** GitHub Issues (open + closed); Pull Requests (open

- merged + closed); GitHub Discussions threads; GitHub Security
  Advisories filed via GHSA workflow; CVE entries the project
  requested through GitHub's CVE Numbering Authority.

**Sensitivity.** Public by virtue of running on a public repository.
Private Vulnerability Reporting (PVR) advisories are private until
published per [`SECURITY.md`](../../SECURITY.md)'s coordinated-disclosure
policy.

**Where it lives.** GitHub (issues + PRs + discussions + advisories);
inherent in repo recovery per BCP §2 Tier 2.

---

## 3. Retention periods per category

The retention durations below are the **policy commitments**. Each
row names the data category from §2, the retention duration, the
disposal method from §1's "What counts as disposal" enumeration, and
the framework that establishes the floor.

**Engineer-as-collaborator gap note.** Where the retention duration
is **enforced by an external mechanism** (e.g., GitHub Actions'
90-day default), the table names that mechanism. Where the retention
duration is **stated intent that is not yet automated**, the table
explicitly notes the enforcement gap. The policy commits to the
intent; closing the enforcement gap is named in §9 (Maintenance) as
a hardening item where one exists.

| Category                                                                              | Retention duration                                                                 | Disposal method                               | Framework floor                                                  | Enforcement mechanism                                                                                                                                                          |
| ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | --------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Source code + git history (§2.1)                                                      | **Indefinite**                                                                     | None — never disposed                         | None (project commitment)                                        | GitHub repository persistence + maintainer-local mirror (per BCP §5 Tier 0); Apache 2.0 license ensures community-side persistence regardless                                  |
| Governance corpus (§2.2)                                                              | **Indefinite**                                                                     | None — superseded versions live in history    | ISO 27001 5.36 (monitoring of policies)                          | Inherent in git history; older versions of any document remain accessible via `git log`                                                                                        |
| Audit-trail artifacts (§2.3) — public repo artifacts                                  | **Indefinite**                                                                     | None — append-only                            | SOC 2 CC2.2 + CC8.1, ISO 27001 8.32                              | Inherent in git history; CHANGELOG + decision-logs grow append-only                                                                                                            |
| Audit-trail artifacts (§2.3) — runtime unified audit log (SaaS instance)              | **Intended: 7 years**                                                              | Cryptographic erasure on backup expiry        | SOC 2 CC7.2, ISO 27001 8.15, HIPAA 164.312(b)                    | **Enforcement gap noted (§3.1 below).** Postgres table is unbounded today; no TTL configured. Policy commits to 7-year intent; closing the gap is in §9                        |
| CI/CD — GitHub Actions logs (§2.4)                                                    | **90 days** (GitHub default; project does not override)                            | Aging out via rotation                        | None (operational; SOC 2 CC7.1 satisfied at retention floor)     | GitHub Actions default retention; documented at [GitHub Actions usage limits](https://docs.github.com/en/actions/learn-github-actions/usage-limits-billing-and-administration) |
| CI/CD — workflow artifact uploads (§2.4)                                              | **90 days** (GitHub default; project does not override per-workflow)               | Aging out via rotation                        | None (operational)                                               | GitHub Actions default `retention-days` parameter                                                                                                                              |
| CI/CD — container images, **tagged releases** (§2.4)                                  | **Indefinite**                                                                     | None — never pruned                           | None (project commitment; SOC 2 CC8.1 reproducibility)           | ghcr.io retains tagged release images; no override configured                                                                                                                  |
| CI/CD — container images, **edge tag (rolling `main`)** (§2.4)                        | **30 days rolling**                                                                | Aging out via rotation                        | None (operational)                                               | [`.github/workflows/edge-image-prune.yml`](../../.github/workflows/edge-image-prune.yml) (slice 207) — weekly prune workflow                                                   |
| CI/CD — scanner finding history (CodeQL, Dependabot, GitGuardian, Trivy, govulncheck) | **Vendor default** (varies by vendor; documented in vendor's own retention policy) | Vendor-controlled                             | None (operational; security tab is forensic)                     | Vendor-managed; the project does not back these up                                                                                                                             |
| Maintainer SaaS — Postgres state (§2.5)                                               | **Indefinite while in use; lost on instance termination**                          | Hard delete on instance termination           | Operator-side (canvas §5.4 RLS constrains cross-tenant disposal) | Maintainer-controlled Docker volume; no external retention floor                                                                                                               |
| Maintainer SaaS — Postgres nightly dumps (§2.5)                                       | **30 days rolling** (per BCP §5 Tier 3)                                            | Aging out via lifecycle rule                  | Operator-side (BCP RPO 24h)                                      | Offsite storage lifecycle rule per `docs/SELF_HOSTING.md`                                                                                                                      |
| Maintainer SaaS — evidence ledger (§2.5)                                              | **Indefinite, append-only**                                                        | Ledger tombstone (append-only-with-supersede) | Canvas invariant #3 (constitutional)                             | Schema-enforced (append-only by policy at the application layer); reads require integrity check (sha256-per-record)                                                            |
| Maintainer SaaS — evidence object storage (§2.5)                                      | **Co-extensive with evidence ledger**                                              | Bucket-level versioning + lifecycle           | Canvas invariant #3 (constitutional)                             | Bucket versioning per `docs/SELF_HOSTING.md`; lifecycle deletes 30-day-old versions                                                                                            |
| Maintainer SaaS — observability stack data (§2.5)                                     | **30 days rolling**                                                                | Aging out via rotation                        | None (operational; SOC 2 CC4.1 satisfied at the rolling window)  | Tempo + Loki default retention configured at deploy time                                                                                                                       |
| Maintainer SaaS — Unraid offsite parity-aware backup (§2.5)                           | **7 days rolling** (per BCP §5 Tier 3)                                             | Aging out via rotation                        | Operator-side                                                    | Unraid OS-managed                                                                                                                                                              |
| Maintainer SaaS — OAuth AS signing keys (§2.5)                                        | **Co-extensive with key validity**; rotated on suspected compromise                | Hard delete on rotation                       | Slice 327 audit M-1 (currently tracked at slice 366)             | **Enforcement gap noted (§3.1).** Manual rotation today; slice 366 commits to automation                                                                                       |
| Third-party-service state (§2.6)                                                      | **Vendor-controlled**                                                              | Vendor-controlled                             | None (the project does not own the retention surface)            | Vendor-managed; documented in each vendor's own retention policy                                                                                                               |
| Issue-tracker state (§2.7) — Issues, PRs, Discussions, advisories                     | **Indefinite**                                                                     | None — never disposed                         | None (project commitment)                                        | GitHub repository persistence                                                                                                                                                  |
| Issue-tracker state (§2.7) — private PVR advisories                                   | **Indefinite once published; private until disclosure**                            | None                                          | None (coordinated disclosure per `SECURITY.md`)                  | GitHub Advisory machinery                                                                                                                                                      |

### 3.1 Enforcement gaps named honestly

Two retention durations in §3 are **stated intent that the project's
current substrate does not enforce automatically**. Naming them here
keeps the policy honest per P0-375-1 (no claims about retention the
platform does not actually enforce) and surfaces them for §9
hardening prioritization.

#### Gap 1: 7-year unified audit log retention is intent, not enforced

The unified audit log table on the maintainer-operated SaaS instance
(per `migrations/sql/20260517000000_unified_audit_log.sql`) is
**unbounded today** — no scheduled job purges rows older than the
intended 7-year floor; the table grows monotonically. SOC 2 CC7.2 and
ISO 27001 8.15 expect audit logs to be retained "for a defined
period" and the 7-year floor is the common Type II auditor
expectation; the policy commits to that floor. Closing the
enforcement gap requires:

1. Documenting the rotation cadence (daily / weekly / monthly job).
2. Implementing a scheduled purge of rows older than the floor while
   preserving the BCP §5 Tier 3 offsite backup of any purged rows
   (purged-rows-are-still-backed-up is the cryptographic-erasure
   posture in §1 "What counts as disposal").
3. Adding integration test coverage that asserts the purge respects
   audit-period freezing per canvas §8.4.

Until these items ship, the unified audit log retention is **"all
records since the table was created (2026-05-17, slice 040), retained
indefinitely"**, which **over-retains** relative to the 7-year intent
rather than under-retaining. Over-retention is acceptable from a
compliance posture; under-retention would be the policy violation.

Named in §9 as a hardening item.

#### Gap 2: OAuth AS signing key rotation is manual, not automated

The OAuth Authorization Server's JWT signing keys (per ADR-0003,
slice 187 D1) are rotated manually today; there is no scheduled
rotation, no rotation-on-suspected-compromise automation. Slice 327
audit M-1 surfaced this; slice 366 commits to fixing it. The
retention column for OAuth AS signing keys in §3 above ("co-extensive
with key validity; rotated on suspected compromise") describes the
intent; the operational reality is "rotation happens when the
maintainer manually executes the keystore-rewrite path documented in
`internal/auth/keystore`."

Until slice 366 lands, the retention posture for the OAuth AS
signing keys is **manual rotation per incident-response §7.2 auth-
compromise playbook** rather than scheduled rotation. The BCP §5
Tier 4 acknowledges this gap; this document acknowledges it again so
the retention picture is complete.

Named in §9 as a hardening item; tracked at slice 366 already.

---

## 4. Disposal procedures

For each category in §2, the disposal procedure is one of the five
methods enumerated in §1 ("What counts as disposal"). The procedures
below are operational; they describe what the maintainer does (or
what the configured machinery does on the maintainer's behalf) when
retention expires.

### 4.1 Hard delete

**Used for.** Stale CI secrets identified at the quarterly access
review (per the [access-review plan §4.1](./access-review.md#41-quarterly-review-procedure)
step 2); revoked PATs; uninstalled GitHub Apps; expired Postgres
dumps removed by the offsite lifecycle rule.

**Procedure.**

1. Identify the artifact and the trigger (scheduled cadence,
   access-review finding, lifecycle-rule expiry).
2. Execute the deletion through the vendor's native deletion
   surface (`gh secret delete`, GitHub UI revocation,
   `rclone delete`, etc.).
3. Document the deletion in the appropriate audit trail:
   - Access-review-driven deletions: per-review artifact (per
     access-review plan §6).
   - Incident-driven deletions: incident log (per IR plan §10).
   - Scheduled lifecycle deletions: not individually logged
     (per §5 below — "Audit trail of disposal").

**Audit-trail commitment.** When the deletion is operator-visible
or has security relevance (e.g., a revoked PAT, a removed
collaborator), a CHANGELOG `### Security` bullet records the
deletion per the access-review plan §6 CHANGELOG discipline.
Routine lifecycle expiries do not file individual CHANGELOG
entries (the volume would flood the change log).

### 4.2 Soft delete with tombstone

**Used for.** Yanked releases; superseded governance documents
(prior versions live in git history); revoked PATs (GitHub retains
the audit record); deprecated CI workflows (kept under
`.github/workflows/` with a deprecation note, or moved to a
`deprecated/` subdirectory pending removal at the annual review).

**Procedure.**

1. Mark the artifact as superseded / yanked / deprecated through
   the appropriate UI or commit.
2. Preserve the original record for audit-trail continuity.
3. Cross-reference the supersession in the audit trail:
   - Yanked releases: CHANGELOG `### Security` entry per IR plan §6.
   - Superseded governance docs: the new version's document-history
     table cross-references the prior version's commit SHA.

**Why the tombstone pattern exists.** Hard-deleting a yanked
release tag would leave operators who pinned to that tag with a
"tag not found" error; soft-deleting (marking as Pre-release or
removing the binary but retaining the tag) preserves the
addressability while flagging the artifact as not-current.

### 4.3 Cryptographic erasure

**Used for.** Encrypted-at-rest backups whose underlying storage
medium cannot be selectively wiped (typically the offsite Postgres
dump retention on a maintainer-controlled S3-compatible store, or
the Unraid offsite parity-aware backup). The slice 327 audit's
verified-positive observation that encryption at rest is enabled
across the backup substrate is the precondition that makes
cryptographic erasure a usable disposal method.

**Procedure.**

1. The data is encrypted at rest with a key the maintainer
   controls.
2. When the data ages out per its retention window, the lifecycle
   rule deletes the ciphertext.
3. Concurrently, when an encryption key rotates (per the
   incident-response plan §7.2 auth-compromise playbook, or per the
   BCP §5 Tier 4 named cadence when slice 366 lands), any
   ciphertext encrypted with the rotated-out key becomes
   cryptographically inaccessible.

**Honest scope.** Cryptographic erasure is **not** a primary
disposal method in the project's current posture; it is the
**fallback** disposal posture for ciphertext that cannot be
selectively deleted. Lifecycle-rule hard deletion is the
operational primary. Cryptographic erasure becomes load-bearing
only when a forensic-integrity question arises — for example,
"after the maintainer rotates the offsite backup encryption key,
are the prior-window backups recoverable?" The honest answer is
"no, by design."

### 4.4 Aging out via rotation

**Used for.** GitHub Actions logs (90-day rolling); workflow
artifact uploads (90-day rolling); edge-tag container images
(30-day rolling per `edge-image-prune.yml`); Postgres nightly dumps
(30-day rolling per BCP §5 Tier 3); Unraid offsite parity-aware
backups (7-day rolling); observability stack data (30-day rolling
on Tempo + Loki).

**Procedure.**

1. The retention window is configured at the substrate (GitHub
   Actions retention setting; bucket lifecycle rule; Tempo / Loki
   retention configuration; `edge-image-prune.yml` weekly run).
2. New artifacts are written; old artifacts age out automatically.
3. The maintainer does not individually execute disposals; the
   configured machinery does.
4. Configuration drift is caught at the annual review (§9) —
   verifying the configured window matches the documented window.

**Audit-trail commitment.** Rotation-based disposal is not
individually logged. The substrate's own retention setting is the
audit evidence (e.g., the screenshot of the GitHub Actions retention
setting; the lifecycle rule's documented configuration). If the
retention window is changed materially (e.g., from 30 days to 60
days), the change ships as a slice + PR + CHANGELOG entry — the
configuration is the policy.

### 4.5 Ledger tombstone (append-only-with-supersede)

**Used for.** The evidence ledger on the maintainer-operated SaaS
instance (and on every operator-hosted deployment); the unified
audit log table; any append-only platform-product surface that
cannot be deleted but can be marked superseded.

**Procedure.**

1. The original record is never deleted — canvas invariant #3
   forbids it.
2. A tombstone record is appended that names the original
   (referencing it by primary key) and the supersede reason
   (artifact-lost, retracted, superseded-by-newer-evidence, etc.).
3. Read queries that should hide superseded records add the
   tombstone-aware filter; queries that need the full audit trail
   omit the filter and see both the original and the tombstone.

**Why this pattern exists.** Canvas invariant #3 (append-only
evidence ledger between ingestion and evaluation stages) is the
constitutional substrate that makes the BCP §6 Scenario C
(object-storage loss) recoverable at all. Hard-deleting evidence
records would defeat the invariant. The ledger tombstone pattern
gives the project a disposal posture that respects the invariant.

**Constitutional load-bearing reference.** Canvas invariant #3 is
the constitutional commitment; this document operationalizes the
disposal posture compatible with it.

---

## 5. Audit trail of disposal

This section governs **when a disposal event is logged**.

### What is logged individually

The following disposal events file individual records (in the form
named):

| Event                                                       | Logged as                                                              | Where                                                                                                                                  |
| ----------------------------------------------------------- | ---------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Access-review-driven revocation                             | `### Security` CHANGELOG bullet + per-review artifact entry            | `CHANGELOG.md` + `docs/governance/access-reviews/YYYY-<period>.md` per [access-review plan §6](./access-review.md#6-audit-trail)       |
| Incident-driven secret rotation                             | Incident log entry + `### Security` CHANGELOG bullet                   | `docs/incidents/YYYY-MM-DD-<slug>.md` per [IR plan §10](./incident-response.md#10-incident-log-template)                               |
| Yanked release                                              | `### Security` CHANGELOG bullet + release-notes correction             | `CHANGELOG.md` + GitHub Releases                                                                                                       |
| Material retention-window configuration change              | Slice + PR + `### Documentation` CHANGELOG bullet                      | Inherent in PR machinery                                                                                                               |
| Audit-log purge (when §3.1 Gap 1 is closed and purge runs)  | Per-purge entry in the unified audit log itself + monthly summary line | `atlas_audit_log` table; quarterly governance check-in summary                                                                         |
| Evidence-ledger tombstone (artifact-lost or retraction)     | Per-tombstone entry in the unified audit log                           | `atlas_audit_log` table (recorded by the platform's tombstone-write code path; full history per canvas invariant #3 + slice 016 drift) |
| OAuth AS signing key rotation (manual today; auto post-366) | Incident log if rotation was incident-driven; otherwise audit-log only | `docs/incidents/` and / or `atlas_audit_log`                                                                                           |

### What is not logged individually

The following disposal events do **not** file individual records;
the substrate's configured retention setting is the audit evidence:

- **CI log expiry at the GitHub Actions 90-day default.** Logging
  every expiry would flood the change log and add no signal beyond
  what the configured setting already conveys.
- **Workflow artifact expiry at the 90-day default.** Same rationale.
- **Edge-tag image prune via `edge-image-prune.yml`.** The workflow
  run history at GitHub Actions is itself the audit evidence; the
  CHANGELOG does not duplicate.
- **Postgres-dump lifecycle expiry at 30 days.** The offsite bucket's
  lifecycle rule is the audit evidence; per-dump expiry is not
  individually logged.
- **Observability stack data expiry at 30 days.** Tempo + Loki
  retention configuration is the audit evidence.
- **Unraid offsite parity-aware backup expiry at 7 days.** The Unraid
  OS's retention configuration is the audit evidence.

### Why the asymmetry

Routine rotation expiries are **expected operations** — the
substrate is configured to behave this way; no human decision is
involved in any individual expiry. Logging them would create noise
without signal. Operator-visible or security-relevant disposals
(revocations, yanks, rotations) involve human decisions and have
post-hoc accountability value; those file individual records.

This asymmetry matches the [access-review plan §6 CHANGELOG
discipline](./access-review.md#changelog-discipline) ("Reviews
that produce no revocations and no scope-reductions do not file a
CHANGELOG entry") and the [BCP plan §10 file-naming](./business-continuity.md#10-documentation-and-audit-trail)
("A small number of continuity events per year is the realistic
baseline; if the cadence exceeds 6 per year, that is a signal worth
investigating") — auditable cadences over per-event logging.

---

## 6. Legal hold override

This section governs **when normal disposal is suspended**.

### What is a legal hold

A **legal hold** under this policy is a maintainer decision to
suspend the disposal of artifacts that would otherwise age out per
§3-§4. Legal holds may be triggered by:

1. **Litigation.** A formal legal request (subpoena, court order,
   discovery demand) requires preservation of relevant artifacts.
2. **Regulatory inquiry.** A regulator with jurisdiction (a
   state attorney general, a data-protection authority, a sector
   regulator) requests preservation pending an investigation.
3. **Active security incident.** Per the [IR plan §5.3](./incident-response.md#53-eradication)
   "eradication" phase — forensic preservation may justify a hold
   on disposal of relevant logs and artifacts until the
   investigation completes.
4. **Maintainer-initiated forensic preservation.** When the
   maintainer judges that a disposal would impair a future
   investigation (e.g., a Dependabot alert on a dependency that
   the maintainer expects may surface as a P0 incident shortly),
   the maintainer may impose a hold preemptively.

### Trigger shape

**Any received subpoena, court order, or written regulatory request
that names retained artifacts as relevant to an investigation
**automatically triggers a legal hold across all categories** of
§2** until the hold is formally released. The trigger is
intentionally broad to reduce the burden on the maintainer of
classifying a request's scope under stress.

For active security incidents and maintainer-initiated holds, the
hold's scope is **named in the incident log or in a dedicated
hold-tracking document**; the hold is not implicitly project-wide.

### Hold scope

When a legal hold is active, the affected categories' disposal
procedures (§4) **are suspended** for the hold's duration. Specifically:

- **Aging-out via rotation** is suspended by extending the
  retention window indefinitely (or by exporting the soon-to-expire
  artifacts to a maintainer-controlled preserved store).
- **Lifecycle-rule deletions** are suspended by disabling the
  offsite lifecycle rule for the affected bucket prefix.
- **Hard deletes** are suspended; access reviews surface revocation
  candidates but the maintainer does not execute the revocation
  while a hold is active (the revocation is queued for execution
  post-hold-release).
- **Ledger tombstones** continue (canvas invariant #3 still applies;
  tombstones are forward-only and do not violate the hold).
- **Soft deletes with tombstone** continue (the original record is
  preserved by definition).

### Release process

A legal hold is released when:

1. **The triggering condition concludes** — the litigation closes,
   the regulator confirms no further preservation is needed, or the
   security incident is fully eradicated per IR plan §5.3.
2. **The maintainer documents the release** in the hold-tracking
   document with a release date and rationale.
3. **A 30-day cooling-off period** elapses between the triggering
   condition concluding and the resumption of normal disposal — to
   guard against premature disposal when an apparently-concluded
   matter resurfaces.

After release + cooling-off, normal disposal resumes from the next
scheduled cycle. Disposal that was queued during the hold (e.g., a
revocation surfaced at an access review that occurred during the
hold) executes per the original retention schedule applied to the
deferred queue.

### Hold tracking

Legal holds are tracked in `docs/governance/legal-holds.md` — a
single Markdown file that lists active and historical holds in
table form. The file is created on first use; until then, this
document's reference is forward-only. The hold-tracking file's
shape:

| Hold ID | Triggered  | Scope                             | Released   | Release rationale          |
| ------- | ---------- | --------------------------------- | ---------- | -------------------------- |
| HOLD-NN | YYYY-MM-DD | What categories / artifacts apply | YYYY-MM-DD | One-line release rationale |

**Confidentiality.** The hold-tracking file is public-by-default
per the same posture as the IR plan §8 and access-review plan §6.
If a hold's existence cannot be publicized (e.g., a court order
includes a non-disclosure clause), the hold is held in the private
archive with `[redacted — see private archive]` in the public
table, mirroring the incident-log redaction pattern.

### Solo-maintainer legal-hold reality

The maintainer is the legal-hold authority. There is no separate
legal counsel on retainer; the maintainer's response to any legal
process is informed by best-effort general-counsel-substitute
research and, when warranted, ad-hoc retention of outside counsel
at the maintainer's expense. This is named here so a third-party
reviewer understands the control's actual shape: legal holds work,
but they work at the speed of a sole maintainer reading and
responding to written requests.

When the [GOVERNANCE.md](../../GOVERNANCE.md) advisory-council
formation trigger fires, this section is re-evaluated. Until
then, single-person legal-hold response is the honest answer.

---

## 7. Solo-maintainer honesty

This section names the **constraints the sole-maintainer reality
imposes on this policy** and the honest substitutes the project
adopts. The pattern mirrors the [IR plan §3 role devolution](./incident-response.md#solo-maintainer-role-devolution),
the [BCP plan §3 role devolution](./business-continuity.md#solo-maintainer-role-devolution),
and the [access-review plan §5 solo-maintainer considerations](./access-review.md#5-solo-maintainer-considerations).

### The retention officer is the maintainer

Frameworks (SOC 2, ISO 27001) typically expect a **named
information governance officer** or **records manager** responsible
for retention-policy execution. The project does not have that
role; the maintainer is the named retention officer by default,
the named records manager by default, and the named legal-hold
authority by default. Per the same pattern as IR / BCP / access-
review:

- **There is no separate retention officer.** The maintainer
  decides what gets retained, what gets disposed, and when.
- **There is no second-pair-of-eyes review of disposal
  decisions.** The annual review of this document is the
  retrospective scrutiny surface — disposals during the past year
  are surfaced and reviewed in aggregate.
- **There is no formal escalation path.** If the maintainer is
  unavailable when a legal hold would normally trigger, see
  [GOVERNANCE.md](../../GOVERNANCE.md) "Bus-factor & succession" —
  the sealed-envelope mechanism the maintainer commits to
  documenting covers this scenario implicitly: the trusted contact
  receiving a legal request for the unavailable maintainer would
  trigger the org-transfer path before any retention decision is
  made.

### Cross-infrastructure-migration commitment

The 7-year unified audit log retention floor in §3 is a commitment
the maintainer must keep **across infrastructure migrations**. If
the Unraid box is replaced (per BCP §6 Scenario A chassis-failure
path), the audit-log data must restore intact from offsite. If the
maintainer migrates from Unraid to a different host platform, the
data must be preserved through the migration. The BCP plan's §6
Scenario A and the §5 Tier 3 backup strategy are the operational
substrate that makes this commitment achievable; this document
**relies on** the BCP plan and is **load-bearing-on** the
maintainer's discipline executing the BCP backup cadence.

### The accumulation problem

A retention policy that says "indefinite" for the governance
corpus, the audit-trail artifacts (public side), the source code,
and the issue-tracker state is **acknowledging that the project's
storage footprint grows monotonically**. This is the right shape
for an OSS project on a public Git repository — the artifacts are
small (Markdown + git history + JSON / YAML), the storage cost is
GitHub's to bear, and the value-of-retention exceeds the cost-of-
storage by orders of magnitude. But it is named here explicitly so
a future maintainer assessing the policy understands the design
trade-off.

When the [GOVERNANCE.md re-evaluation trigger](../../GOVERNANCE.md)
fires (2028-05-20 OR 100 deployed self-hosts) and the project's
operational model is re-examined, the indefinite retention posture
is one of the items re-evaluated alongside the funding posture
and the hosted-SaaS option.

### When the role-stacking becomes untenable

Same trigger as the IR / BCP / access-review plans: the
[GOVERNANCE.md](../../GOVERNANCE.md) advisory-council formation
trigger (≥ 3 outside contributors with ≥ 6 months sustained
involvement). When that trigger fires:

1. The maintainer designates a co-retention-officer.
2. This section is updated to name the rotation.
3. The legal-hold authority is re-evaluated — designating a
   secondary authority for the case where the maintainer is
   unavailable.

Until then, single-person retention-officer is the honest answer.

---

## 8. Cross-references

This section consolidates the constitutional and operational
commitments other parts of the project make that this document
relies on.

### Constitutional commitments (immutable, this document references)

- **Canvas invariant #3** — the Evidence SDK's append-only
  evidence ledger between ingestion and evaluation stages. This
  invariant **constrains** what disposal looks like for evidence
  records: the ledger tombstone pattern (§4.5) is the disposal
  method compatible with the invariant; hard-deleting evidence
  records is forbidden. Slice 016's freshness + drift schemas
  (`migrations/sql/2026...028_evidence_freshness_drift.sql`)
  operationalize the constraint at the database layer.
- **Canvas invariant #6** — PostgreSQL Row-Level Security at the
  database layer. This invariant means that **disposal of one
  tenant's data does not affect another tenant's data**: when an
  operator running the platform disposes of a tenant's records,
  RLS ensures the disposal is scoped. The migration comment at
  `migrations/sql/20260521010000_tenants_rename.sql:195` flags
  that tenant-removal retention semantics are a separate slice;
  this document cross-references but does not constrain that
  slice's design.
- **Canvas §4.6.5 AI-assist boundary (explicit)** — schema-level
  enforcement that `ai_assisted=true` records cannot have
  `human_approved=true` without `human_approver` set. The
  audit-log requirement (full prompt + full response, every time)
  for board-narrative AI-assist (per [`CLAUDE.md`](../../CLAUDE.md)
  "Board-narrative AI-assist" decisions D1-D7) imposes
  **additional immutability requirements** on board-narrative
  draft records: prompt-version + model-name + model-version +
  model-provider must be retained with the draft for the
  lifetime of the draft, and the draft itself is immutable once
  approved (snapshot-at-generation, not retroactive). This
  document's retention rows for the maintainer-operated SaaS
  instance evidence ledger (§3) are co-extensive with these
  AI-assist immutability commitments where the records overlap.
- **Canvas §4.6.7 board-narrative AI-assist** — the seven
  sub-decisions locked together so the worst-case LLM output
  remains acceptable. The full prompt + full response audit
  trail is forensically airtight; storage cost is small
  (few KB per section); retention is co-extensive with the
  board-narrative draft itself, which is indefinite per the
  immutability requirement. This document does not extend or
  modify those commitments; it acknowledges their existence and
  the resulting retention shape.

### Operational commitments (other governance documents, this document composes with)

- **Slice 327 audit verified-positive observations** — encryption
  at rest enabled; no `InsecureSkipVerify` in TLS configuration;
  no logging of sensitive material in production; Argon2id RFC
  9106 parameters at the documented baseline; sha256-per-record
  integrity in the evidence ledger. These verified-positive
  controls **enable** the cryptographic-erasure disposal posture
  (§4.3) — without encryption at rest, cryptographic erasure
  would not be a usable disposal method.
- \*\*[IR plan §6 (Documentation and audit trail)](./incident-response.md#8-documentation-and-audit-trail)
  - §10 (Incident log template)** — incident logs live at
    `docs/incidents/YYYY-MM-DD-<slug>.md` per the IR plan template;
    retention is **indefinite\*\* under this document's §3 audit-trail
    artifacts category. The IR plan and this document share the
    public-by-default + `[redacted — see private archive]` posture.
- \*\*[BCP plan §5 (Backup strategy)](./business-continuity.md#5-backup-strategy)
  - §11 hardening items\*\* — the BCP plan committed to the
    30-day Postgres-dump rolling retention and the 7-day Unraid
    offsite rolling retention. This document reproduces those
    windows in §3 so the retention picture is centralized; the BCP
    plan is the source of truth for the operational shape, this
    document is the source of truth for the policy commitment.
- \*\*[Access-review plan §6 (Audit trail)](./access-review.md#6-audit-trail)
  - §4 (Review procedure)** — per-review artifacts live at
    `docs/governance/access-reviews/YYYY-<period>.md`; retention is
    **indefinite\*\* under this document's §3 audit-trail artifacts
    category. Revocations file `### Security` CHANGELOG bullets;
    empty reviews stay in-artifact. This document reproduces the
    CHANGELOG discipline for consistency.

### Audit binding

- **Slice 329 finding H-4** — this document closes the finding.
  The slice 329 audit explicitly named slice 375 as the spillover
  for the retention-and-disposal policy gap.
- **Slice 372 finding (IR plan)** — chains into this document
  via the audit-trail-artifact retention table row.
- **Slice 373 finding (BCP plan)** — chains into this document
  via the backup retention rows.
- **Slice 374 finding (access-review plan)** — chains into this
  document via the per-review artifact retention row.

---

## 9. Maintenance

### Review cadence

This document is reviewed **annually** by the maintainer,
co-scheduled with the [IR plan](./incident-response.md), the
[BCP plan](./business-continuity.md), and the
[access-review plan](./access-review.md) tabletop. The next
review is due **2027-05-28** — same date as the first annual
access review per the [access-review plan §3](./access-review.md#3-review-cadence-per-tier).

The annual review surfaces:

- Categories in §2 that have appeared since the last review (new
  data classes the project produces or stores).
- Retention durations in §3 that proved unworkable in practice —
  either too aggressive (forced reluctance to dispose) or too
  lenient (compliance auditor flagged at a quarterly check-in).
- Enforcement gaps in §3.1 that have been closed (the unified
  audit log purge mechanism shipped; OAuth AS rotation automation
  shipped via slice 366); the gap-noted rows are updated.
- Disposal procedures in §4 that did not match real-world
  execution (a routine lifecycle expiry that surfaced as a
  surprise; a hard-delete that should have been a soft-delete).
- Legal holds in §6 that came into force during the past year —
  any post-hold-release retrospective.
- Cross-references in §8 that have drifted (e.g., if the BCP plan
  changed its backup-retention windows; if the IR plan changed
  its incident-log file shape).

The review's output is a PR that updates this file plus an annual
review note at `docs/audit-log/data-retention-review-YYYY.md`
(shared documentation pattern with the IR / BCP / access-review
plan annual reviews).

### Ownership

The project maintainer owns this document. Changes follow the
standard slice / PR / DCO process documented in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md). Changes that materially
**reduce** a retention duration (e.g., from 7 years to 3 years on
the unified audit log) require an ADR, because reducing retention
is the easier compliance violation to make accidentally.

### Relationship to ISO 27001 5.36

ISO 27001 5.36 ("Monitoring, review and change management of
information security") expects governance policies to be reviewed
on a fixed cadence with documented results. The annual review
cadence above is the project's commitment to that clause for the
retention surface, mirroring the matching commitments in the IR
plan §12, the BCP plan §11, and the access-review plan §9. Per
the slice 329 audit report §9, this commitment is recorded as a
capability, not as a certification claim.

### Named hardening items (not committed today)

The following items would materially improve the project's
retention-and-disposal posture. They are **named here for
visibility** so the maintainer's annual review surfaces them for
prioritization. Each is named with the gap it closes. None are
committed in this slice; some are tracked at other slices already.

| Item                                                                | Gap it closes                                                                                                                                                                                          | Status                                                                                                                                                                         |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Unified audit log purge mechanism (close §3.1 Gap 1)**            | Today the `atlas_audit_log` table grows monotonically; no scheduled job purges rows older than the 7-year floor. Closing the gap brings actual behavior in line with stated intent                     | Named; not committed. Closing the gap requires: scheduled purge job, configurable retention window, integration test that purge respects audit-period freezing per canvas §8.4 |
| **OAuth AS signing key rotation automation (close §3.1 Gap 2)**     | Today rotation is manual; slice 327 audit M-1 surfaced this                                                                                                                                            | **Tracked at slice 366** (committed work; not yet scheduled). When 366 lands, §3.1 Gap 2 closes                                                                                |
| **Legal-hold tracking document (`docs/governance/legal-holds.md`)** | The file is created on first use per §6; until first use, the project's hold-tracking posture is forward-only                                                                                          | Named; not committed in this slice. Created when first hold occurs                                                                                                             |
| **Retention-configuration drift detection at the annual review**    | The annual review (§9) is procedural; nothing automatically detects if a retention window has drifted from documented value (e.g., GHA retention silently changed)                                     | Named; not committed. Could ship as a script that snapshots configured retention values for the seven controlled substrates                                                    |
| **Cross-infrastructure-migration retention verification**           | §7 names the cross-infrastructure-migration commitment; nothing automatically verifies that a substrate migration preserved retention                                                                  | Named; not committed. Manual verification is the operational substitute today                                                                                                  |
| **PostgreSQL WAL archival** (also named in BCP §11)                 | Tightens the SaaS instance's Postgres RPO below 24 hours; relevant to retention because partial-day data loss between dumps is a retention gap of <1 day rather than 1 day                             | Named in BCP §11; not committed today                                                                                                                                          |
| **Off-GitHub repository mirror** (also named in BCP §11)            | Tier 0 single-substrate risk; relevant to retention because if GitHub disappears and the maintainer-local mirror is lost concurrently, the indefinite-retention posture for governance corpus degrades | Named in BCP §11; not committed today                                                                                                                                          |

### When to deviate from this plan

This plan describes the default retention and disposal posture.
The maintainer may deviate when conditions demand it — for
example:

- **A retention extension** when a compliance auditor or
  legal-hold trigger requires it (covered procedurally in §6).
- **A retention reduction** when storage cost crosses a
  prohibitive threshold for a specific data class (would require
  an ADR per the "Ownership" subsection above).
- **A disposal-method change** when a substrate's available
  disposal surface changes (e.g., GitHub introduces a different
  retention-configuration UI; the project's choice of disposal
  method may evolve).

Deviations are documented in the document-history table below
with a one-line rationale, per the IR plan §11 pattern.

---

## Document history

| Date       | Change                  | Slice |
| ---------- | ----------------------- | ----- |
| 2026-05-28 | Initial document filed. | 375   |
