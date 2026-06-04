# Project asset inventory

**Status:** Active governance document.
**Filed:** 2026-05-28 by slice 376.
**Closes:** Slice 329 compliance meta-audit finding **H-5** (no project-level asset inventory document). Completes the slice 329 governance-doc chain (372 IR plan → 373 BCP plan → 374 access-review plan → 375 data-retention policy → **376 asset inventory**).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual, co-scheduled with the [incident-response plan](./incident-response.md), [business-continuity plan](./business-continuity.md), [access-review plan](./access-review.md), and [data-retention plan](./data-retention.md) tabletop. Next review: 2027-05-28. Trigger-based out-of-band reviews per §6.

---

## Why this document exists

The first question a third-party security reviewer asks before any
technical control evidence is examined is: **"list all assets in
scope."** The platform's technical control surface is exceptionally
strong (verified at slice 327's security audit). The gap that slice
329's compliance meta-audit identified as **H-5** is that the project
had no single document a reviewer could read top-to-bottom to know
**what the project owns, who is responsible for each thing, and how
critical each thing is to project continuity**.

SOC 2 **CC3.2** (Risk assessment — identifies and analyzes risk),
ISO 27001 **5.9** (Inventory of information and other associated
assets), and NIST CSF **ID.AM-1 / ID.AM-2** (Asset Management —
physical devices, systems, software, platforms and applications are
inventoried) all expect a documented asset inventory before scoping
an audit. Without it, a reviewer cannot scope the engagement; with
it, the conversation skips to substantive control evidence.

This document fills that gap. It is the project's standing answer to
that opening question.

This document **describes capabilities**, not certifications.
security-atlas is not SOC 2 certified, not ISO 27001 certified, not
HIPAA-attested. The inventory below documents what the project
currently owns and depends on; it does not claim third-party
attestation of any control.

Cross-references:

- [`docs/governance/incident-response.md`](./incident-response.md) — §2 (severity tiering) cross-references back to this inventory's criticality tiers (§4). Tier 1 assets in this document are the assets whose loss/compromise would justify a P0 incident under the IR plan.
- [`docs/governance/business-continuity.md`](./business-continuity.md) — §4's "BCP working subset" of assets was the seed for this inventory. The §4 table in the BCP plan is now superseded by this document and converted to a pointer (see slice 376 D1 in the decisions log).
- [`docs/governance/access-review.md`](./access-review.md) — §2 access-surface enumeration overlaps with this inventory's Cryptographic Material and Third-Party Integrations categories. The two surfaces compose: access-review cadence is the periodic check on **who can use** an asset; this inventory documents **what the asset is**.
- [`docs/governance/data-retention.md`](./data-retention.md) — §2's seven data categories overlap with this inventory's Documentation Surfaces and Audit-Trail-Artifact rows. Retention policy answers "how long does the artifact persist?"; this inventory answers "what is the artifact?"
- [`GOVERNANCE.md`](../../GOVERNANCE.md) — the maintainer-led posture and bus-factor reality this inventory is honest about. Every Owner column entry resolves to "maintainer" in solo-mode.
- [`SECURITY.md`](../../SECURITY.md) — inbound vulnerability reporting; intake surface that draws on credentials in this inventory (the `security@` email is itself an asset).
- [`CHANGELOG.md`](../../CHANGELOG.md) — the running history of changes is itself an audit-trail asset (Tier 2 below).
- [`docs/adr/0003-oauth-authorization-server.md`](../adr/0003-oauth-authorization-server.md) — the OAuth Authorization Server substrate whose signing keys are inventoried in §3 (Cryptographic Material).
- [`docs/adr/0005-branch-protection-pat-vs-app.md`](../adr/0005-branch-protection-pat-vs-app.md) — names the `BRANCH_PROTECTION_READ_TOKEN` PAT inventoried in §3.
- [`Plans/canvas/01-vision.md`](../../Plans/canvas/01-vision.md) §6 — the v1 binary success criterion ("survive a third-party security review") that this document is load-bearing for.
- Canvas **invariant #3** (Evidence SDK push-only contract; append-only evidence ledger) — the ledger itself is a Tier 1 asset on the maintainer-operated SaaS instance.
- Canvas **invariant #6** (PostgreSQL Row-Level Security at the database layer) — the RLS-enforced table state is the multi-tenant integrity asset; loss/compromise of the RLS policy set is a Tier 1 event.
- [`docs/audits/327-security-audit-security-auditor-report.md`](../audits/327-security-audit-security-auditor-report.md) — verified-positive controls that inform criticality grading.
- [`docs/audits/329-compliance-meta-audit-report.md`](../audits/329-compliance-meta-audit-report.md) — the audit finding H-5 that filed this slice.

---

## 1. Purpose and scope

### What "asset" means in this document

An **asset** under this inventory is anything the project owns,
operates, depends on, or holds custody of where its loss, compromise,
or unavailability would impair the project's ability to operate or
the operator-side compliance posture this governance-doc suite
documents. Specifically:

1. **Code and configuration the project authors** (source code,
   workflow definitions, schema migrations, in-tree governance docs).
2. **Distribution artifacts the project produces** (container
   images, release tags, the docs site).
3. **Cryptographic material the project holds custody of** (signing
   keys, OAuth client secrets, deploy tokens, webhook secrets).
4. **Infrastructure the project runs on** (the maintainer-operated
   SaaS instance, CI runners, container registries, the issue
   tracker).
5. **Third-party services the project integrates with** (vendor
   accounts for code coverage, secret scanning, dependency scanning,
   IdP).
6. **Communication surfaces the project speaks through**
   (`SECURITY.md` intake, `security@` mailbox, governance documents,
   the CHANGELOG).

The cut-line for "is this an asset?" is **operational consequence**:
if losing it requires a documented restore path (per BCP plan §6), a
revocation procedure (per IR plan §7), a rotation event (per
data-retention plan §4), or an access-review entry (per
access-review plan §2), it is inventoried here.

### What this plan covers

This document is **scope: project-side**. Specifically:

- The `mgoodric/security-atlas` repository and every artifact derived
  from it.
- The maintainer-operated SaaS instance — the single-host Unraid
  deployment the maintainer runs for personal use (defined in BCP
  plan §1.2).
- Every third-party service the project (not operators using the
  product) authenticates against.
- The cryptographic material custodianship surface for project-
  controlled secrets.
- The governance corpus that documents the project's commitments to
  third-party reviewers.

### What this plan does not cover

- **Per-customer SaaS instance assets.** Operators self-hosting
  security-atlas inventory their own assets per their own programs.
  Their inventory is the FrameworkScope / tenant-scope half of the
  platform data model (canvas §5.1-§5.5); this document is the
  project's own inventory, not a template operators must adopt.
- **Tenant data inside the maintainer-operated SaaS instance.**
  Canvas invariant #6 (RLS at the database layer) is the runtime
  isolation substrate; tenant-specific data is the operator's data.
  This document covers the substrate (the Postgres instance, the
  ledger, the object store) as project assets; the tenant rows
  inside those substrates are out of scope per the same posture as
  the data-retention plan §1.
- **The maintainer's personal infrastructure.** The maintainer's
  workstation, password manager vendor, hardware token model,
  network architecture, and other personal-IT surfaces are out of
  scope per [P0-376-2](../issues/376-project-asset-inventory.md).
  Where load-bearing (e.g., the maintainer-local Git mirror named
  in BCP plan §5 Tier 0), the asset is referenced by **role**, not
  by **vendor / model / configuration detail**.
- **Secret values verbatim.** Per P0-376-1, this inventory lists
  asset **names, types, owners, criticality** — not values, paths,
  or exploitation-aiding detail. The slice 329 audit's "no exploit-
  roadmap detail" boundary (D10) governs everything in §3.
- **Third-party-service customer-data.** When the project consumes
  Codecov / GitGuardian / Dependabot, the asset is **the project's
  account** at that vendor — not the data the vendor holds on
  behalf of other customers.

### Engineer-as-collaborator scope note

The work-order references "project-personal Unraid box (192.168.1.246)"
in the orchestrator's example asset list. **The internal IP is
omitted from this published inventory** — naming the IP address
would constitute exploit-roadmap detail (slice 329 D10) for a
publicly-readable governance document. The asset is named
categorically as "**Maintainer-operated SaaS instance — single-host
Unraid deployment**", consistent with BCP plan §1 / §6 Scenario A
wording. The maintainer holds the operational connectivity detail
privately. This is the same name-the-asset-without-publishing-the-
exploit-path discipline the access-review plan applied to webhook
URLs and CI secret values.

---

## 2. Asset categories

The project's asset surface groups into **six categories** for
inventory purposes. The categories are calibrated so that within
each, ownership and criticality follow uniform patterns; across
categories, the criticality floor is set by the most-load-bearing
asset in the category.

### 2.1 Code repositories

The source-of-truth surface for everything the project produces.
Every other category derives, depends on, or operates on the
content of this category.

**Examples.** The `mgoodric/security-atlas` monorepo on GitHub
(every commit, every branch, every tag, every release); the
maintainer-local mirror per BCP §5 Tier 0; every contributor's
clone (which is itself a recovery substrate under the Tier 0
distributed-by-design model).

**Criticality floor.** Tier 1 (project-stopping if lost). However,
loss is **infeasible** by design: every clone is a full mirror.
The Apache-2.0 license (per GOVERNANCE.md) ensures community-side
persistence.

### 2.2 Container images

The deployment-time artifacts operators pull from the project's
container registry. Reproducible from tagged commits (so loss
recovers via rebuild — see BCP plan §6 Scenario E + §5 Tier 1) but
non-trivial to rebuild without source access.

**Examples.** Per-binary tags at `ghcr.io/mgoodric/security-atlas`:
the platform binary (`atlas`), the CLI binary (`atlas-cli`), the
OSCAL bridge (`oscal-bridge`), the OpenAPI codegen binary
(`atlas-openapi`), the rolling `edge` tag (managed by the slice 207
`edge-image-prune.yml` workflow per data-retention plan §3).

**Criticality floor.** Tier 2 (significant degradation if lost,
not project-stopping — rebuild is possible).

### 2.3 Cryptographic material

Signing keys, secrets, tokens, and credentials the project holds
custody of. Loss/compromise of any Tier 1 entry here is a P0
incident under IR plan §7.2 (auth compromise) and triggers
BCP plan §6 Scenario D or E.

**Examples categorically** (specific identifiers withheld per
P0-376-1; values held in maintainer's password manager and on the
maintainer-operated SaaS instance's encrypted secrets surface):

- **OAuth Authorization Server JWT signing keys** (ES256 per
  slice 187 D1; per [ADR-0003](../adr/0003-oauth-authorization-server.md)).
- **Evidence-ledger cosign signing keys** (when slice 368 lands —
  for container-image attestation chain).
- **OSCAL-export `cosign-kms` signing key** (slice 413 / 368a Phase 1):
  a cloud-KMS-held key (AWS KMS / GCP KMS / Azure Key Vault / Vault
  transit) referenced by `ATLAS_COSIGN_KMS_REF`, used at runtime to sign
  OSCAL audit-export bundles in `cosign-kms` mode. Custody is the
  **operator's** (the key lives in the operator's cloud KMS, not in
  maintainer custody) — atlas holds only a _reference_ + `kms:Sign`
  permission, never the private key. Air-gap deployments use the
  hermetic `embedded-ed25519` mode instead and hold no such key. See
  [ADR-0010](../adr/0010-oscal-cosign-signing.md) and
  `docs/runbooks/oscal-signing.md`.
- **Bundled `cosign` binary** (slice 413, AC-10): the OSCAL `cosign-kms`
  mode shells out to the upstream `cosign` binary. **Provenance:**
  sigstore/cosign, **version pinned `v3.0.6`** (same version + pin the
  release-signing job uses, `.github/workflows/release.yml` via
  `sigstore/cosign-installer@v4.1.2`; the CI integration job installs the
  same pin). **License: Apache-2.0** — bundle-clean per ADR-0010 cost
  ledger (permissive; permits binary redistribution with LICENSE/NOTICE
  preserved). Not a secret/key — listed here as the tracked third-party
  dependency on the signing path. A version bump is a documented
  maintenance task (368 notes), not a silent change.
- **GitHub Personal Access Tokens** that the project uses,
  including the `BRANCH_PROTECTION_READ_TOKEN` per
  [ADR-0005](../adr/0005-branch-protection-pat-vs-app.md).
- **GitHub Deploy keys** configured against the repository.
- **Webhook secrets** for receivers the project operates.
- **Container registry push tokens** issued by GitHub's package
  machinery (ephemeral per workflow run; not persisted).
- **OIDC RP client secret** for the maintainer-operated SaaS
  instance.
- **The maintainer's GPG signing key** used for DCO sign-off and
  release-tag signatures.
- **CI secret store** (`secrets.*` references in
  `.github/workflows/*.yml`): release-please App private key,
  Codecov upload token, Homebrew tap publishing token, integration-
  test database bootstrap password, GitHub-native `GITHUB_TOKEN`
  (refreshed per workflow-run, no rotation required).
- **`security@` mailbox credentials** (per
  [SECURITY.md](../../SECURITY.md)) — the intake surface for
  coordinated disclosure.

**Criticality floor.** Tier 1 for any credential whose compromise
enables authoring/yanking releases, modifying `main`, or
impersonating the project (signing keys, OAuth client secret,
admin-level PATs). Tier 2 for read-only or narrowly-scoped
credentials.

### 2.4 Deploy infrastructure

Physical and virtual infrastructure the project operates or
operates on. Loss triggers BCP plan §6 Scenarios A-C.

**Examples.**

- **Maintainer-operated SaaS instance — single-host Unraid
  deployment.** The platform running for the maintainer's personal
  use (per BCP plan §1; not a public commercial offering). Holds:
  PostgreSQL instance, evidence object storage (S3-compatible),
  observability stack (Prometheus + Tempo + Loki + Grafana + OTel
  collector), Docker compose state.
- **CI runner pool.** The GitHub Actions runner fleet the project
  consumes (vendor-managed by GitHub Actions). The project does
  not run self-hosted runners as of this filing; if that changes,
  self-hosted runners become a Tier 2 asset in their own right.
- **Container registry.** `ghcr.io/mgoodric/security-atlas`
  (GitHub-managed; project consumes).
- **Docs site hosting.** GitHub Pages (GitHub-managed; project
  consumes).
- **GitHub repository hosting.** Per category §2.1.
- **The maintainer-local Git mirror.** Per BCP plan §5 Tier 0;
  the load-bearing recovery substrate for full GitHub-loss
  scenarios.

**Criticality floor.** Tier 1 for the SaaS instance's data tier
(Postgres state, object storage) and for GitHub repository
hosting; Tier 2 for the observability stack and CI runner
availability.

### 2.5 Third-party integrations

External services the project authenticates against and consumes.
Loss tends to be vendor-recovered (BCP plan §5 Tier 4); the
project's exposure is **vendor account state** rather than
project-owned data.

**Examples.**

- **GitHub Actions** — CI runner pool; workflow execution
  substrate.
- **GitHub-native Security tab** — CodeQL, Dependabot, secret-
  scanning findings, GitHub Security Advisories, CVE Numbering
  Authority workflow.
- **Codecov** — coverage upload + history.
- **GitGuardian** — secret scanning on push.
- **Dependabot** — dependency vulnerability surfacing (also
  GitHub-native).
- **CodeQL** — static analysis (also GitHub-native).
- **StepSecurity Harden-Runner** — CI hardening hook.
- **release-please App** — release automation.
- **OpenID Connect IdP** — TBD; the operator's choice. When the
  maintainer-operated SaaS instance is deployed against an IdP, the
  IdP-vendor account is a Tier 1 asset (loss of OIDC config means
  no human can log in to the SaaS instance).

**Criticality floor.** Tier 2 for vendor-recovered scanner state
(Codecov / GitGuardian / etc. — re-scannable from `main`); Tier 1
for the IdP integration that gates the SaaS instance.

### 2.6 Documentation surfaces

The deliberate communications the project publishes to
contributors, adopters, and third-party reviewers. The
authoritative answer to "what does the project commit to?" lives
here.

**Examples.**

- **Repo-root community-health files:** `LICENSE`, `README.md`,
  `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `GOVERNANCE.md`.
- **Architectural decision records** at `docs/adr/`.
- **Canvas under `Plans/`** — the system-of-record for design intent.
- **Governance corpus** at `docs/governance/` — including this
  document and the four sibling slices (372 + 373 + 374 + 375).
- **Audit reports** at `docs/audits/`.
- **Audit decision logs** at `docs/audit-log/`.
- **Slice docs** at `docs/issues/`.
- **CHANGELOG.md** at the repo root — itself an audit-trail asset
  (per data-retention plan §3 audit-trail-artifacts retention is
  indefinite).
- **Docs site** rendered from the canvas + governance corpus to
  GitHub Pages via the slice 058 mkdocs Material substrate.

**Criticality floor.** Tier 2 (significant degradation if lost,
since the governance commitments would need to be reconstructed).
Loss is **infeasible** by design — the documents live in the repo
and inherit Tier 1 distributed-by-design persistence.

---

## 3. Per-asset detail table

This section is the document's center of gravity. It enumerates the
specific assets in each category from §2 with owner, criticality,
location/identifier, backup status, access surface, and lifecycle
status.

**Per P0-376-1, no sensitive identifiers are listed verbatim.** Where
an asset has a publicly-discoverable name (a repository URL, a
container registry path, a docs-site URL), that name is included.
Where an asset has a secret value (a PAT, a webhook secret, an OAuth
client secret), only the **role** of the credential is named; the
value is held in the maintainer's password manager and on the SaaS
instance's encrypted secrets surface.

### 3.1 Code repositories (Category 2.1)

| Asset                                  | Tier | Owner       | Location/identifier                         | Backup status                                                                          | Access surface                                                                      | Lifecycle status |
| -------------------------------------- | ---- | ----------- | ------------------------------------------- | -------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ---------------- |
| `mgoodric/security-atlas` monorepo     | 1    | Maintainer  | github.com/mgoodric/security-atlas          | (a) GitHub primary; (b) maintainer-local `git clone --mirror` weekly per BCP §5 Tier 0 | Access-review plan §2.1 (repo collaborators) + §2.7 (PATs) — see access-review plan | Active           |
| Maintainer-local Git mirror            | 1    | Maintainer  | Maintainer workstation (path withheld)      | Inherent (encrypted disk; FileVault)                                                   | Maintainer-only physical access                                                     | Active           |
| Tagged releases (release-asset bundle) | 1    | Maintainer  | github.com/mgoodric/security-atlas/releases | Inherent in Tier 0 mirror + GitHub Releases                                            | Read: public; Write: maintainer + release-please App                                | Active           |
| Contributor clones                     | 1    | Distributed | Per contributor                             | Inherent (every clone is a full mirror)                                                | Per-contributor; Apache-2.0 license grants the recovery-substrate role              | Active           |

### 3.2 Container images (Category 2.2)

| Asset                                   | Tier | Owner      | Location/identifier                             | Backup status                                                                  | Access surface                                            | Lifecycle status |
| --------------------------------------- | ---- | ---------- | ----------------------------------------------- | ------------------------------------------------------------------------------ | --------------------------------------------------------- | ---------------- |
| Platform binary image (`atlas`)         | 2    | Maintainer | `ghcr.io/mgoodric/security-atlas/atlas`         | Not backed up as blob; recovery via rebuild-from-tag per BCP §5 Tier 1         | Read: public; Write: release-please workflow + maintainer | Active           |
| CLI binary image (`atlas-cli`)          | 2    | Maintainer | `ghcr.io/mgoodric/security-atlas/atlas-cli`     | Not backed up as blob; recovery via rebuild-from-tag                           | Read: public; Write: release-please workflow + maintainer | Active           |
| OSCAL bridge image (`oscal-bridge`)     | 2    | Maintainer | `ghcr.io/mgoodric/security-atlas/oscal-bridge`  | Not backed up as blob; recovery via rebuild-from-tag                           | Read: public; Write: release-please workflow + maintainer | Active           |
| OpenAPI codegen image (`atlas-openapi`) | 2    | Maintainer | `ghcr.io/mgoodric/security-atlas/atlas-openapi` | Not backed up as blob; recovery via rebuild-from-tag                           | Read: public; Write: release-please workflow + maintainer | Active           |
| Edge rolling tag                        | 3    | Maintainer | `ghcr.io/mgoodric/security-atlas/*:edge`        | Rolling 30-day window per `.github/workflows/edge-image-prune.yml` (slice 207) | Read: public; Write: CI on push to `main`                 | Active           |

### 3.3 Cryptographic material (Category 2.3)

| Asset                                           | Tier | Owner      | Location/identifier (role only — values not published)                                                         | Backup status                                                                                     | Access surface                                                                                               | Lifecycle status                                                          |
| ----------------------------------------------- | ---- | ---------- | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| OAuth AS JWT signing keys (ES256 per slice 187) | 1    | Maintainer | Maintainer-operated SaaS instance keystore (`internal/auth/keystore`)                                          | **Not backed up** — recovery posture is rotate (BCP §5 Tier 4); manual today, slice 366 automates | Runtime: SaaS instance only; Rotation: maintainer manually (slice 366 will automate)                         | Active (rotation cadence intent: per slice 366)                           |
| Evidence-ledger cosign signing key              | 1    | Maintainer | Maintainer custody (when slice 368 lands)                                                                      | **Not backed up** — recovery posture is rotate; design in slice 368                               | Sign: slice 368 release workflow; Verify: public (operator side)                                             | Planned (slice 368 not yet shipped)                                       |
| OSCAL-export `cosign-kms` key (slice 413/368a)  | 1    | Operator   | Operator cloud KMS, referenced by `ATLAS_COSIGN_KMS_REF` (atlas holds a reference + `kms:Sign`, never the key) | Operator-managed (KMS-native; out of maintainer scope)                                            | Sign: runtime OSCAL `cosign-kms` export; Verify: public via stock `cosign verify-blob`                       | Active (Phase 1 shipped; opt-in. Air-gap uses `embedded-ed25519`, no key) |
| `BRANCH_PROTECTION_READ_TOKEN` (PAT)            | 2    | Maintainer | GitHub repo `secrets.BRANCH_PROTECTION_READ_TOKEN` per ADR-0005                                                | **Not backed up** — recovery posture is regenerate per ADR-0005                                   | Use: branch-protection-drift CI job; Rotation: ADR-0005 default cadence (annual)                             | Active                                                                    |
| Other GitHub PATs (maintainer-account scoped)   | 1-2  | Maintainer | Maintainer's GitHub PAT page (per access-review plan §2.7 + §4.3 Step 1)                                       | **Not backed up** — recovery posture is regenerate                                                | Use: maintainer-driven scripted operations + CI per workflow; Annual access review per access-review plan §3 | Active inventory at every annual review                                   |
| GitHub Deploy keys                              | 2    | Maintainer | `gh api repos/mgoodric/security-atlas/keys` (annual review per access-review plan §4.3 Step 3)                 | **Not backed up** — recovery posture is regenerate                                                | Use: deployment hosts; Annual review per access-review plan                                                  | Annually inventoried                                                      |
| Webhook secrets                                 | 2    | Maintainer | Per-receiver shared secret (held in maintainer's password manager)                                             | **Not backed up** — recovery posture is regenerate                                                | Use: per webhook receiver; Annual review per access-review plan §2.6                                         | Active inventory at every annual review                                   |
| Container registry tokens (per-workflow)        | 3    | GitHub     | Ephemeral per-workflow-run via `GITHUB_TOKEN`                                                                  | N/A — regenerated per run                                                                         | Use: release workflow only                                                                                   | Refreshed per workflow run                                                |
| OIDC RP client secret                           | 1    | Maintainer | Maintainer's password manager + SaaS instance env-var surface                                                  | Password-manager-side (vendor-managed); maintainer offline copy in encrypted form                 | Use: SaaS instance OIDC RP at `internal/auth/oidc/oidc.go`                                                   | Active; rotated on suspected compromise per IR plan §7.2                  |
| Maintainer GPG signing key                      | 1    | Maintainer | Maintainer custody; public half at `github.com/mgoodric.gpg`                                                   | Maintainer offline encrypted backup (personal-IT scope — held privately per P0-376-2)             | Sign: DCO commits + release tags; Verify: public                                                             | Active; annual review confirms still under maintainer control             |
| release-please App private key                  | 1    | Maintainer | GitHub repo `secrets.*` (per access-review plan §2.4)                                                          | **Not backed up** — recovery posture is regenerate via GitHub App settings                        | Use: release workflow only                                                                                   | Active                                                                    |
| Codecov upload token                            | 2    | Maintainer | GitHub repo `secrets.*`                                                                                        | **Not backed up** — vendor-regenerable                                                            | Use: CI coverage upload                                                                                      | Active                                                                    |
| Homebrew tap publishing token                   | 2    | Maintainer | GitHub repo `secrets.*`                                                                                        | **Not backed up** — vendor-regenerable                                                            | Use: release workflow; Homebrew formula publish step                                                         | Active                                                                    |
| Integration-test database bootstrap password    | 3    | Maintainer | GitHub repo `secrets.*`                                                                                        | N/A — test-scope only; never reaches production                                                   | Use: CI integration test job; Postgres bootstrap                                                             | Active                                                                    |
| GitHub-native `GITHUB_TOKEN`                    | N/A  | GitHub     | Per-workflow injected; never persisted                                                                         | N/A — GitHub-managed                                                                              | Use: per-workflow; expires at workflow run end                                                               | N/A (GitHub-managed)                                                      |
| `security@` mailbox                             | 1    | Maintainer | Per SECURITY.md (specific address held in repo `SECURITY.md`)                                                  | Mailbox-vendor-side (vendor-managed)                                                              | Receive: inbound vuln reports per SECURITY.md; Read: maintainer                                              | Active                                                                    |

### 3.4 Deploy infrastructure (Category 2.4)

| Asset                                           | Tier | Owner          | Location/identifier                                                                           | Backup status                                                                                  | Access surface                                                                              | Lifecycle status                |
| ----------------------------------------------- | ---- | -------------- | --------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- | ------------------------------- |
| Maintainer-operated SaaS instance — Unraid host | 1    | Maintainer     | Maintainer-controlled single-host (IP / hostname held privately per P0-376-2 + slice 329 D10) | Parity disk (real-time) + offsite parity-aware backup (7-day rolling) per BCP §5 Tier 3        | Use: maintainer SSH + Unraid web UI; no public exposure beyond the platform's published API | Active                          |
| SaaS instance — Postgres state                  | 1    | Maintainer     | Postgres on Unraid host (docker volume)                                                       | `pg_dump` nightly to offsite S3-compatible store per BCP §5 Tier 3 (RPO 24h)                   | Runtime: SaaS instance internal; Operator queries: `atlas-cli` + UI                         | Active                          |
| SaaS instance — evidence object storage         | 1    | Maintainer     | S3-compatible bucket on Unraid host                                                           | Bucket-level versioning + lifecycle rules + nightly offsite replication per BCP §5 Tier 3      | Runtime: SaaS instance via Evidence SDK push path (canvas §4.1)                             | Active                          |
| SaaS instance — append-only evidence ledger     | 1    | Maintainer     | Postgres on SaaS instance + sha256-per-record integrity (canvas invariant #3 substrate)       | Inherent in Postgres backup (above); canvas §3 is the recovery substrate per BCP §6 Scenario C | Runtime: SaaS instance; Write: Evidence SDK push only                                       | Active (constitutional surface) |
| SaaS instance — RLS policy set                  | 1    | Maintainer     | PostgreSQL row-security policies on tenant-scoped tables (canvas invariant #6)                | Inherent in Postgres backup + schema migration history (`migrations/sql/*.up.sql`)             | Schema-level enforcement (no runtime credential bypasses RLS by design)                     | Active (constitutional surface) |
| SaaS instance — observability stack             | 2    | Maintainer     | Prometheus + Tempo + Loki + Grafana + OTel collector on Unraid host (docker volumes)          | 30-day rolling per data-retention plan §3; state loss is loss-acceptable                       | Use: maintainer Grafana access                                                              | Active                          |
| SaaS instance — docker-compose state            | 2    | Maintainer     | `deploy/docker/docker-compose.yml` (in-repo source-of-truth) + running Docker on Unraid       | Source: in-repo (Tier 0); runtime: rebuilt from compose file                                   | Use: maintainer operator commands                                                           | Active                          |
| CI runner pool                                  | 2    | GitHub Actions | GitHub Actions runner fleet (GitHub-managed)                                                  | Vendor-managed; project does not back up                                                       | Use: per workflow-run; no persistent state                                                  | Active (vendor-recovered)       |
| Container registry hosting                      | 2    | GitHub         | `ghcr.io/mgoodric/security-atlas`                                                             | Inherent in tagged-commit reproducibility (BCP §6 Scenario E)                                  | Read: public; Write: release workflow                                                       | Active                          |
| GitHub repository hosting                       | 1    | GitHub         | github.com/mgoodric/security-atlas                                                            | Maintainer-local mirror per BCP §5 Tier 0 (RPO 0 by distributed-by-design)                     | Per access-review plan §2                                                                   | Active                          |
| GitHub Pages docs site hosting                  | 2    | GitHub         | docs.mgoodric.github.io or per-domain (per slice 058 mkdocs config)                           | Source in repo; rendered output is regenerable per BCP §5 Tier 2                               | Read: public; Write: `.github/workflows/docs.yml`                                           | Active                          |

### 3.5 Third-party integrations (Category 2.5)

| Asset                                                       | Tier | Owner      | Location/identifier                                       | Backup status                                                                                      | Access surface                                                               | Lifecycle status                                                                                                                                                   |
| ----------------------------------------------------------- | ---- | ---------- | --------------------------------------------------------- | -------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| GitHub Actions workflow runs + logs                         | 2    | Maintainer | GitHub Actions (vendor-hosted)                            | 90-day rolling per GitHub default (data-retention plan §3)                                         | Use: maintainer triage; CI workflow consumption                              | Active                                                                                                                                                             |
| GitHub-native Security tab (CodeQL, Dependabot, advisories) | 2    | Maintainer | GitHub-managed                                            | Vendor-managed; project does not back up                                                           | Use: maintainer security review; CVE assignment via CNA workflow             | Active                                                                                                                                                             |
| Codecov account                                             | 2    | Maintainer | codecov.io account configured for mgoodric/security-atlas | Vendor-recovered (re-uploadable from CI rerun)                                                     | Use: CI coverage upload via slice 159 sqlc-toolchain config                  | Active                                                                                                                                                             |
| GitGuardian account                                         | 2    | Maintainer | gitguardian.com account configured for the repository     | Vendor-recovered (re-scans active branches on reconnection)                                        | Use: secret scanning on push                                                 | Active                                                                                                                                                             |
| StepSecurity Harden-Runner                                  | 2    | Maintainer | Per slice 117 audit-mode hook in `.github/workflows/*`    | Vendor-managed; configuration in-repo (Tier 0)                                                     | Use: CI hardening hook                                                       | Active (audit mode)                                                                                                                                                |
| release-please App                                          | 2    | Maintainer | GitHub App installed on mgoodric/security-atlas           | Vendor-managed; key per §3.3 Tier 1                                                                | Use: release automation                                                      | Active                                                                                                                                                             |
| dependabot                                                  | 2    | Maintainer | GitHub-native; `.github/dependabot.yml` in-repo           | Vendor-managed; config in-repo (Tier 0)                                                            | Use: weekly dependency-update PRs                                            | Active                                                                                                                                                             |
| CodeQL                                                      | 2    | Maintainer | GitHub-native; workflow at `.github/workflows/codeql.yml` | Vendor-managed; config in-repo                                                                     | Use: weekly + per-push static analysis                                       | Active                                                                                                                                                             |
| Trivy + govulncheck                                         | 2    | Maintainer | Pinned in CI (`cmd/scripts/coverage-gate`, workflows)     | In-repo configuration                                                                              | Use: per-build vulnerability scanning                                        | Active                                                                                                                                                             |
| **OpenID Connect IdP (operator's choice)**                  | 1    | Operator   | Per-deployment configuration (NOT bundled by project)     | **Operator-responsibility; this inventory does not name the IdP** for the maintainer-SaaS instance | Use: human authentication into the SaaS instance per slice 198 first-install | **Engineer-as-collaborator gap note:** the maintainer-SaaS instance's specific IdP choice is held privately per P0-376-2; the role is a Tier 1 asset categorically |

### 3.6 Documentation surfaces (Category 2.6)

| Asset                                       | Tier | Owner      | Location/identifier                                                                 | Backup status                                                              | Access surface                                                            | Lifecycle status |
| ------------------------------------------- | ---- | ---------- | ----------------------------------------------------------------------------------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------- | ---------------- |
| `LICENSE`                                   | 1    | Maintainer | `/LICENSE` at repo root (Apache 2.0)                                                | Inherent in repo (Tier 0)                                                  | Read: public                                                              | Active           |
| `README.md`                                 | 2    | Maintainer | `/README.md` at repo root                                                           | Inherent in repo                                                           | Read: public                                                              | Active           |
| `SECURITY.md`                               | 2    | Maintainer | `/SECURITY.md` at repo root                                                         | Inherent in repo                                                           | Read: public; intake routes inbound vuln reports                          | Active           |
| `CONTRIBUTING.md`                           | 2    | Maintainer | `/CONTRIBUTING.md` at repo root                                                     | Inherent in repo                                                           | Read: public                                                              | Active           |
| `CODE_OF_CONDUCT.md`                        | 2    | Maintainer | `/CODE_OF_CONDUCT.md` at repo root                                                  | Inherent in repo                                                           | Read: public                                                              | Active           |
| `GOVERNANCE.md`                             | 2    | Maintainer | `/GOVERNANCE.md` at repo root                                                       | Inherent in repo                                                           | Read: public                                                              | Active           |
| `CHANGELOG.md`                              | 2    | Maintainer | `/CHANGELOG.md` at repo root (audit-trail asset per data-retention §3)              | Inherent in repo                                                           | Read: public; Write: release-please + per-PR Conventional Commit messages | Active           |
| `CLAUDE.md`                                 | 2    | Maintainer | `/CLAUDE.md` at repo root (constitutional principles per session bootstrap)         | Inherent in repo                                                           | Read: public                                                              | Active           |
| Architectural decision records              | 2    | Maintainer | `docs/adr/*.md`                                                                     | Inherent in repo                                                           | Read: public                                                              | Active           |
| Canvas (system-of-record for design intent) | 2    | Maintainer | `Plans/canvas/*.md` + `Plans/ARCHITECTURE_CANVAS.md` + companion deep-dives         | Inherent in repo                                                           | Read: public                                                              | Active           |
| Governance corpus (this category)           | 2    | Maintainer | `docs/governance/*.md` (this document + sibling slices 372 + 373 + 374 + 375 + 182) | Inherent in repo                                                           | Read: public                                                              | Active           |
| Audit reports                               | 2    | Maintainer | `docs/audits/*.md`                                                                  | Inherent in repo                                                           | Read: public                                                              | Active           |
| Audit decision logs                         | 2    | Maintainer | `docs/audit-log/*.md`                                                               | Inherent in repo                                                           | Read: public                                                              | Active           |
| Slice docs                                  | 2    | Maintainer | `docs/issues/*.md`                                                                  | Inherent in repo                                                           | Read: public                                                              | Active           |
| Incident logs                               | 2    | Maintainer | `docs/incidents/*.md` (per IR plan §10)                                             | Inherent in repo (public-by-default with redaction posture per IR plan §8) | Read: public; private archive for redactions                              | Active           |
| Access-review evidence artifacts            | 2    | Maintainer | `docs/governance/access-reviews/*.md` (per access-review plan §6)                   | Inherent in repo                                                           | Read: public; Write: maintainer at scheduled cadence                      | Active           |
| Docs site (rendered)                        | 2    | Maintainer | GitHub Pages (per slice 058 mkdocs Material)                                        | Source in repo; rendered output regenerable                                | Read: public                                                              | Active           |

---

## 4. Criticality tiering rubric

Every asset in §3 carries a tier from the three-tier rubric below.
The tiers map criticality to the consequence-class of loss/compromise
and bind the IR plan severity tiers, the BCP plan RTO/RPO targets,
and the access-review cadence.

| Tier  | Definition                                                            | Examples (illustrative, not exhaustive)                                                                                                                              | Bound to (IR / BCP / access-review)                                                                                       |
| ----- | --------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| **1** | Loss / compromise = **project-stopping**                              | Source code repository; SaaS instance Postgres + evidence ledger + RLS policy set; OAuth AS signing keys; GPG signing key; OIDC RP client secret; cosign signing key | IR plan P0 severity; BCP plan Tier 0 / Tier 3 (RTO 24h / RTO 4h respectively); access-review quarterly (where applicable) |
| **2** | Loss / compromise = **significant degradation**                       | Container images; CI runners; observability stack; governance corpus; PATs; webhook secrets; non-primary credentials                                                 | IR plan P1 severity; BCP plan Tier 1 / Tier 2 / Tier 4 (RTO 24h-7d-48h respectively); access-review semi-annual or annual |
| **3** | Loss / compromise = **inconvenience** (replaceable; vendor-recovered) | Ephemeral CI container registry tokens; integration-test database bootstrap password; edge rolling-tag images; observability data older than 30 days                 | IR plan P2 / P3 severity; BCP plan no dedicated tier (covered by ephemerality); access-review annual or N/A               |

### Why three tiers and not more

The three-tier shape matches the IR plan's severity tiers
(P0 / P1 / P2-P3 collapsed to two operational shapes) and the BCP
plan's five RTO/RPO tiers (Tier 0 + Tier 3 → asset Tier 1; Tier 1 +
Tier 2 + Tier 4 → asset Tier 2; ephemerals → asset Tier 3). A
finer rubric would multiply categorization decisions without
materially improving the access-review cadence binding, which is
the operational consequence of the tier.

### Counting Tier 1 assets

The asset inventory in §3 contains the following Tier 1 entries
(across all categories):

- **Source code repository** (3.1): `mgoodric/security-atlas`,
  maintainer-local mirror, tagged releases, contributor clones —
  **4 assets**.
- **Cryptographic material** (3.3): OAuth AS JWT signing keys,
  cosign signing key (planned), non-trivially-scoped GitHub PATs,
  OIDC RP client secret, maintainer GPG signing key, release-please
  App private key, `security@` mailbox — **7 assets**.
- **Deploy infrastructure** (3.4): SaaS instance Unraid host,
  Postgres state, evidence object storage, evidence ledger, RLS
  policy set, GitHub repository hosting — **6 assets**.
- **Third-party integrations** (3.5): OpenID Connect IdP — **1 asset**.
- **Documentation surfaces** (3.6): `LICENSE` — **1 asset** (the
  Apache 2.0 license file itself; loss/compromise of `LICENSE`
  could deny operators the legal grant to run the platform —
  project-stopping for operators).

**Total Tier 1 assets: 19** across the six categories.

### Tier rationale at the per-category level

- **Source code is Tier 1 across the board because loss is
  infeasible by design** — every clone is a full mirror — but the
  inventory captures the asset's criticality so that BCP plan §6
  Scenario E's GitHub-loss recovery procedure is itself defensible
  ("the asset is Tier 1; here is the documented restoration path
  that holds RPO 0").
- **Cryptographic material splits across Tier 1 (project-stopping)
  and Tier 2 (significant degradation)** because compromise of a
  signing key enables impersonation (project-stopping); compromise
  of a narrow-scoped PAT enables limited unauthorized actions.
  The Tier 1 / Tier 2 split per credential is recorded in §3.3.
- **Deploy infrastructure SaaS-instance components are Tier 1
  because compromise of any one of them (host, Postgres, object
  store, ledger, RLS) breaks the canvas invariant chain.** The
  observability stack is Tier 2 because its state is loss-
  acceptable per BCP plan §5 Tier 3 footnote.
- **Third-party integrations are mostly Tier 2 (vendor-recovered)
  with one Tier 1 exception** — the OIDC IdP gates SaaS instance
  authentication; loss = no human can log in.
- **Documentation is mostly Tier 2 (significant degradation; loss
  is infeasible by design) with one Tier 1 exception** — the
  `LICENSE` file. Without it, operators do not have the legal
  grant to run the platform.

---

## 5. Review cadence and trigger-based reviews

This document is reviewed **annually**, co-scheduled with the four
sibling tabletops at **2027-05-28**. The annual review surfaces:

- Assets that have appeared since the last review (a new third-
  party integration; a new signing-key surface; a new
  infrastructure component).
- Assets that have been retired or replaced (a third-party vendor
  the project no longer uses; a credential the project no longer
  holds).
- Criticality re-evaluations (an asset whose tier should adjust
  based on operational experience).
- Owner re-assignments (if the GOVERNANCE.md advisory-council
  trigger has fired and ownership delegation is appropriate).
- Cross-references to other governance documents that have
  drifted (e.g., if the access-review plan's §2 enumeration
  changes; if the BCP plan's §4 working-table conversion needs
  updating).

### Trigger-based out-of-band reviews

In addition to the annual cadence, an out-of-band review is
triggered when any of the following occurs:

1. **A new third-party integration is added.** When the project
   begins consuming a new vendor service (e.g., a new CI scanner,
   a new release-publishing vendor, a new container-image
   verification service), the new asset's row is added to §3.5
   and §4 criticality is assessed within 7 days.
2. **A new key custodianship is taken on.** When the project takes
   custody of a new cryptographic asset (slice 368's cosign signing
   key when it lands; a future JWT key rotation schema per slice
   366), the new asset's row is added to §3.3 and §4 criticality
   is assessed within 7 days.
3. **A new infrastructure component is added.** When the project
   adds infrastructure (a new container-image build surface; a
   self-hosted CI runner; a non-Unraid hosting move for the SaaS
   instance per BCP plan §6 Scenario A), the new asset's row is
   added to §3.4 within 7 days.
4. **An asset is retired.** When a previously-inventoried asset is
   retired (a deprecated vendor; a removed CI integration; a
   rotated-out signing key class), the asset's row's lifecycle
   status flips from `Active` to `Retired` and the retirement is
   recorded in the document-history table at the bottom of this
   document within 7 days.
5. **A new GitHub App is installed against the repository.** This
   condition is also a §7.2 access-review trigger; the access-
   review out-of-band evidence artifact cross-references the
   inventory update.
6. **A material change in criticality.** If an asset's
   loss/compromise consequence class changes (e.g., the SaaS
   instance moves from personal-use to multi-tenant — at which
   point the GOVERNANCE.md re-evaluation trigger fires anyway),
   the tier is updated within the next applicable scheduled
   review.

Out-of-band reviews produce a PR with the inventory update +
a one-line entry in the document-history table; they do not file
a separate per-review artifact (this differs from the access-
review plan's per-review-artifact discipline because inventory
updates are simpler — the diff is the audit evidence).

### Slip handling

A late annual review is a documented event, not a hidden one. The
slip provision matches the access-review plan §3:

1. If the annual review slips past its target date, the maintainer
   files a one-line entry in the next-applicable update PR.
2. The review is performed at the earliest feasible date after the
   slip.
3. If two consecutive annual cycles slip, the maintainer escalates
   per GOVERNANCE.md bus-factor & succession.

---

## 6. Cross-references to sibling governance documents

This section consolidates the explicit relationships between this
inventory and the four sibling slices that complete the slice 329
governance-doc chain.

### 6.1 Slice 372 — Incident Response plan

- **IR plan §2 severity tiers bind to inventory criticality tiers.**
  Loss/compromise of an inventory Tier 1 asset is a candidate for
  IR plan P0 severity. Loss/compromise of a Tier 2 asset is a
  candidate for IR plan P1.
- **IR plan §4 detection inventory** ("what is monitored") names
  the asset surfaces this inventory categorizes. The two
  documents share vocabulary.
- **IR plan §7 per-tier playbooks** reference specific inventory
  assets — §7.1 (secret committed to repo) operates on assets
  inventoried in §3.3 here; §7.2 (auth compromise) operates on
  the maintainer's GitHub credentials inventoried in §3.3; §7.3
  (dependency vulnerability) operates on the supply-chain
  surface inventoried in §3.5; §7.4 (deploy break) operates on
  the release pipeline inventoried across §3.2 and §3.4.

### 6.2 Slice 373 — Business Continuity / Disaster Recovery plan

- **BCP plan §4 working-table conversion (D1 in decisions log).**
  The BCP plan's §4 carries a working-asset-inventory table that
  was explicitly marked as a working subset pending slice 376.
  **D1 in the decisions log records the engineer's decision** about
  whether to (a) convert the BCP §4 table to a pointer to this
  inventory, (b) leave the BCP §4 table in place as the BCP-
  operational working view with a cross-reference to this
  inventory, or (c) update the BCP §4 table inline to match this
  inventory's resolution.
- **BCP plan §5 backup strategy** rows reference assets inventoried
  here by tier — Tier 1 assets here align with BCP §5 Tier 0 / Tier
  3 backup posture; Tier 2 align with BCP §5 Tier 1 / Tier 2 / Tier 4.
- **BCP plan §6 restore scenarios** operate on specific inventory
  assets — Scenario A on §3.4 SaaS-instance Unraid host; Scenario B
  on §3.4 Postgres state; Scenario C on §3.4 evidence object
  storage and §3.4 evidence ledger; Scenario D on the full SaaS-
  instance asset chain; Scenario E on §3.1 GitHub repository
  hosting + maintainer-local mirror + §3.3 GitHub-account
  credentials.

### 6.3 Slice 374 — Access review plan

- **Access-review plan §2.1-§2.8 access surfaces** overlap heavily
  with §3.3 cryptographic material and §3.4 deploy infrastructure
  here. The two documents take different cuts at the same surface:
  - **Access-review plan** is the periodic check on **who can use**
    a credential / surface (continue / revoke / reduce-scope).
  - **This inventory** is the catalog of **what credentials /
    surfaces exist**.
- **Access-review plan §2.4 CI secret store** corresponds to §3.3
  here's CI secret entries (release-please key, Codecov token,
  Homebrew tap token, integration-test DB password). The access-
  review plan walks the secrets quarterly; this inventory carries
  them by category.
- **Access-review plan §2.7 PATs** corresponds to §3.3 here's PAT
  entries (`BRANCH_PROTECTION_READ_TOKEN`, other maintainer-account
  PATs). Annual review per access-review plan §3.

### 6.4 Slice 375 — Data retention + disposal policy

- **Data-retention plan §2 seven data categories** map to this
  inventory's six asset categories at varying levels:
  - §2.1 source code + git history ↔ §3.1 code repositories here.
  - §2.2 governance corpus ↔ §3.6 documentation surfaces here.
  - §2.3 audit-trail artifacts ↔ §3.6 documentation surfaces here
    (the public artifacts) + §3.4 SaaS-instance state (the
    runtime unified audit log).
  - §2.4 CI/CD artifacts ↔ §3.2 container images here + §3.5
    third-party integrations.
  - §2.5 maintainer-operated SaaS instance state ↔ §3.4 deploy
    infrastructure here.
  - §2.6 third-party-service state ↔ §3.5 third-party integrations
    here.
  - §2.7 issue-tracker state ↔ subset of §3.6 (GitHub Issues +
    PRs + Discussions are documentation surfaces in a broad
    sense; the data-retention plan treats them as their own
    category for retention purposes).
- **Data-retention plan §3 retention durations** answer "how long
  does the asset persist?"; this inventory answers "what is the
  asset?". The two documents are read together to get both halves.

---

## 7. Solo-maintainer honesty

This section names the **constraints the sole-maintainer reality
imposes on this inventory** and the honest substitutes the project
adopts. The pattern mirrors the [IR plan §3 role devolution](./incident-response.md#solo-maintainer-role-devolution),
the [BCP plan §3 role devolution](./business-continuity.md#solo-maintainer-role-devolution),
the [access-review plan §5 solo-maintainer considerations](./access-review.md#5-solo-maintainer-considerations),
and the [data-retention plan §7 solo-maintainer honesty](./data-retention.md#7-solo-maintainer-honesty).

### Every asset's owner devolves to the maintainer

The Owner column in §3 resolves to "Maintainer" for every project-
controlled asset. There is no separate asset custodian; there is no
records-management function; there is no infrastructure-operations
team. The maintainer is the named asset owner by default. Per the
same pattern as IR / BCP / access-review / data-retention:

- **There is no separate asset custodian.** The maintainer is
  the holder for every credential, every signing key, every
  infrastructure component, every third-party-service account.
- **There is no second-pair-of-eyes on inventory updates.** The
  annual review is the retrospective scrutiny surface for the
  inventory's accuracy.
- **There is no formal escalation path during a real loss event.**
  If the maintainer is unavailable when an asset loss occurs, see
  GOVERNANCE.md "Bus-factor & succession" — the sealed-envelope
  mechanism the maintainer commits to documenting carries the
  organizational-recovery path that operates on the assets
  inventoried here.

### The inventory is itself an asset

This document — `docs/governance/asset-inventory.md` — is itself a
Tier 2 documentation-surface asset (§3.6 row). Loss/compromise of
this inventory would impair the project's compliance posture (no
documented answer to "list all assets in scope") but the loss is
**infeasible by design** because the document lives in the repo
and inherits Tier 0 distributed-by-design persistence.

This recursive property — the inventory is one of the things being
inventoried — is named here for transparency. It is not a defect.

### Re-evaluation when bus-factor improves

When the [GOVERNANCE.md advisory-council formation trigger](../../GOVERNANCE.md)
fires (≥ 3 outside contributors with ≥ 6 months sustained
involvement), this section is revised to name the council's role
in asset ownership. Specifically:

1. Co-ownership of high-criticality assets (signing keys,
   credentials, SaaS instance) is delegated to a designated co-
   maintainer.
2. The §3 Owner column is updated to name the rotation.
3. The §7.2 trigger condition (new key custodianship) is updated
   to reflect the multi-custodian model.

Until the trigger fires, single-person asset ownership is the
honest answer.

---

## 8. §N hardening items (not committed today)

The following items would materially improve the project's
asset-inventory posture. They are **named here for visibility** so
the maintainer's annual review surfaces them for prioritization.
Each is named with the gap it closes. None are committed in this
slice; some are tracked elsewhere already.

| Item                                                                         | Gap it closes                                                                                                                                                                                                    | Status                                                                                                                                                         |
| ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Programmatic GitHub Apps + OAuth Apps inventory snapshot**                 | Today §3.5's third-party-integration rows are maintained manually; a `gh api repos/mgoodric/security-atlas/installation` snapshot at every annual review would surface drift                                     | Named; not committed. Composes naturally with access-review plan §8 "Diff-against-previous-review automation"                                                  |
| **Programmatic secret inventory snapshot**                                   | Today §3.3 CI secrets are listed categorically; a `gh secret list` snapshot at every annual review would surface secrets that have been added without inventory update                                           | Named; not committed. Composes with access-review plan §4.1 Step 2                                                                                             |
| **Asset-criticality binding script**                                         | Today the §4 tier rubric is manually applied; a script that proposes Tier 1/2/3 from heuristics (e.g., is the asset in BCP plan §6 restore-scenario table? is the asset in IR plan §7 playbook?) would help      | Named; not committed                                                                                                                                           |
| **`SECURITY-ACKNOWLEDGEMENTS.md` stub creation**                             | Per slice 329 audit Low finding L-2, SECURITY.md forward-references a file that does not exist; this inventory does not list the file because it does not exist; closing this would add a §3.6 documentation row | Named; not committed in this slice. Audit-report-only per slice 329 disposition (Lows kept audit-report-only)                                                  |
| **Maintainer-personal-infrastructure inventory (private)**                   | This document scopes out personal-IT per P0-376-2; a separate maintainer-private inventory document (held in encrypted form) covering workstation + password manager + hardware token would close the gap        | Named; not committed in this slice. **Maintainer's personal-IT scope; outside this project's published governance.** Mentioned here only so the gap is visible |
| **Off-GitHub repository mirror** (also named in BCP §11 + data-retention §9) | Single-substrate risk in Tier 0 — maintainer-local mirror is the only fallback; if lost concurrently with GitHub, recovery is degraded                                                                           | Named; not committed. Composes with BCP §11 + data-retention §9                                                                                                |
| **Repository-to-organization migration** (also named in access-review §8)    | A user-owned repo cannot use GitHub's organization-level audit log; migration would give the project a richer access-audit surface                                                                               | Named; not committed in this slice. Material project-governance decision; depends on GOVERNANCE.md re-evaluation trigger                                       |
| **Slice 366 JWT signing key rotation automation** (also named everywhere)    | Closes §3.3 OAuth AS JWT signing keys row's "manual today" lifecycle status                                                                                                                                      | **Tracked at slice 366** (committed work; not yet scheduled)                                                                                                   |
| **Slice 368 cosign migration** (also named in BCP §11 + data-retention §9)   | Closes §3.3 cosign signing key row's "planned" lifecycle status                                                                                                                                                  | **Tracked at slice 368** (committed work; not yet scheduled)                                                                                                   |
| **§3 Owner column delegation when advisory-council forms**                   | §7 names the bus-factor reality; closing the gap requires the GOVERNANCE.md advisory-council trigger to fire and a co-maintainer to onboard                                                                      | Named; not committed. Depends on the GOVERNANCE.md trigger                                                                                                     |

### Engineer-as-collaborator gap surface

In building this inventory, the engineer encountered the following
load-bearing assets whose detailed identifiers were intentionally
withheld (per P0-376-1 + slice 329 D10) but whose existence and
role are documented above:

- The SaaS instance's specific IP / hostname (held privately;
  category §3.4).
- The OIDC IdP vendor used by the maintainer-SaaS instance (held
  privately; category §3.5).
- Specific webhook URL paths if any exist as Tier 2 secrets
  (categorical row only in §3.3).
- Specific `security@` mailbox address (lives in SECURITY.md;
  not re-published here per the same role-not-value discipline).

**No adjacent-inventorying gap was discovered** where the project
holds a load-bearing asset that is not categorized above. Every
asset surface the work-order enumerated was inventoried; every
asset the BCP plan §4 working table listed was inventoried; every
access-review plan §2 surface was inventoried.

---

## 9. Maintenance

### Review cadence

This document is reviewed **annually** by the maintainer,
co-scheduled with the [IR plan](./incident-response.md), the
[BCP plan](./business-continuity.md), the
[access-review plan](./access-review.md), and the
[data-retention plan](./data-retention.md) tabletops. The next
review is due **2027-05-28**.

The annual review surfaces:

- Assets that have appeared since the last review.
- Assets that have been retired or replaced.
- Criticality re-evaluations.
- Owner re-assignments when the bus-factor improves.
- Cross-reference drift across the four sibling docs.
- Hardening items in §8 that have been closed.

The review's output is a PR that updates this file plus an annual
review note at `docs/audit-log/asset-inventory-review-YYYY.md`
(shared documentation pattern with the IR / BCP / access-review /
data-retention plan annual reviews).

### Ownership

The project maintainer owns this document. Changes follow the
standard slice / PR / DCO process documented in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md). Changes that materially
**remove** an asset from inventory (e.g., the project retires a
vendor and the asset is removed) require an entry in the document-
history table at the bottom of this document, including the
retirement date and the rationale.

### Relationship to ISO 27001 5.36

ISO 27001 5.36 ("Monitoring, review and change management of
information security") expects governance policies to be reviewed
on a fixed cadence with documented results. The annual review
cadence above is the project's commitment to that clause for the
asset-inventory surface, mirroring the matching commitments in the
IR plan §12, the BCP plan §11, the access-review plan §9, and the
data-retention plan §9. Per the slice 329 audit report §9, this
commitment is recorded as a capability, not as a certification
claim.

### Relationship to ISO 27001 5.9 specifically

ISO 27001 5.9 ("Inventory of information and other associated
assets") is the clause this document closes. The clause expects:
"An inventory of information and other associated assets,
including owners, shall be developed and maintained." The §3
per-asset detail table is the inventory; the Owner column is the
ownership commitment; the §5 + §9 review cadence is the
maintenance commitment.

### When to deviate from this plan

This plan describes the default inventory shape. The maintainer
may deviate when conditions demand it — for example, a
particularly sensitive asset whose existence cannot be published
publicly (the asset would be held in a private addendum referenced
from this document by category-and-role-only). Deviations are
documented in the document-history table with a one-line rationale,
per the IR plan §11 pattern.

---

## Document history

| Date       | Change                  | Slice |
| ---------- | ----------------------- | ----- |
| 2026-05-28 | Initial document filed. | 376   |
