# Business continuity & disaster recovery plan

**Status:** Active governance document.
**Filed:** 2026-05-28 by slice 373.
**Closes:** Slice 329 compliance meta-audit finding **H-2** (no documented Business Continuity / Disaster Recovery plan).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual, co-scheduled with the [incident-response plan](./incident-response.md) tabletop. Next review: 2027-05-28.

---

## Why this document exists

[`SECURITY.md`](../../SECURITY.md) and the
[incident-response plan](./incident-response.md) describe how the project
detects and responds to incidents.
[`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) describes how an **operator**
backs up a self-hosted deployment (`pg_dump` nightly to an S3-compatible
store; artifact bucket versioning + lifecycle rules; configuration in a
private repo). Neither answers the third question a third-party
diligence reviewer asks: **what is the project's own RTO/RPO and how
would it recover its own properties if they were lost?**

This document fills that gap. It is the project's standing answer to
"what happens if your GitHub repo is suspended", "what happens if your
container registry goes down", "what happens if the maintainer is hit by
a bus", and "what is your RTO?"

This document **describes capabilities**, not certifications.
security-atlas is not SOC 2 certified, not ISO 27001 certified, not
HIPAA-attested. The targets below document what the project commits to
today; they do not claim third-party attestation of that commitment.

Cross-references:

- [`docs/governance/incident-response.md`](./incident-response.md) — the response side of recovery. Detect / contain / eradicate is in the IR plan; restore / resume is here. Tabletop exercises are co-scheduled.
- [`GOVERNANCE.md`](../../GOVERNANCE.md) — bus-factor & succession is the load-bearing input to §3 (Roles) and §7 (Continuity of the OSS project).
- [`SECURITY.md`](../../SECURITY.md) — inbound vulnerability reporting; the intake surface the IR plan feeds from.
- [`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) — operator-side backup guidance (NOT this plan's scope — operators run their own BCP).
- [`Plans/canvas/01-vision.md`](../../Plans/canvas/01-vision.md) §1.5 ("Replacement-grade — measurable acceptance criteria") — the v1 binary success criterion this document is load-bearing for.
- Canvas invariant **#3** (Evidence SDK push-only contract; **append-only evidence ledger** between ingestion and evaluation stages) — load-bearing for Scenario C (object-storage loss): the ledger is the replay substrate.
- Canvas invariant **#6** (Row-Level Security at the database layer) — load-bearing for Scenario B (Postgres restore): `tenant_id` integrity must be preserved across the restore.
- [`docs/adr/0003-oauth-authorization-server.md`](../adr/0003-oauth-authorization-server.md) — the OAuth Authorization Server substrate; key-custody concerns in §6 Scenario E reference this ADR's signing-key trust assumptions.
- [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) — the chaos-experiment design backlog that doubles as the self-test substrate for this plan.
- [`docs/audits/327-security-audit-security-auditor-report.md`](../audits/327-security-audit-security-auditor-report.md) — verified-positive controls that inform §5 (Backup strategy) — encryption at rest, no `InsecureSkipVerify`, sha256-per-evidence-record integrity.

---

## 1. Purpose and scope

### What this plan covers

This plan is about continuity-of-operations and disaster recovery for
**the security-atlas project itself**. Specifically:

- The `mgoodric/security-atlas` GitHub repository, releases, and
  container registry (`ghcr.io/mgoodric/security-atlas`).
- The docs site (GitHub Pages, mkdocs-material) and the CI/CD pipeline.
- The maintainer-operated SaaS instance — a single-host Unraid
  deployment that runs the platform as a personal-use SaaS for the
  maintainer (NOT a public product). When this document says
  "**the SaaS instance**", it means this single maintainer-hosted
  deployment.
- Project-controlled third-party services (Codecov, GitGuardian, the
  GitHub Actions runner pool the project consumes, the dependency
  registries the project relies on).
- The key custody and rotation surface for project-controlled signing
  material — release-tag signatures, container image signing (when
  slice 368 cosign migration lands), OIDC RP client secret, JWT
  signing keys, and the BRANCH_PROTECTION_READ_TOKEN PAT documented
  by [ADR-0005](../adr/0005-branch-protection-pat-vs-app.md).

### What this plan does not cover

- **Continuity inside operator-hosted deployments.** Operators
  self-hosting security-atlas are responsible for their own BCP/DR
  plan. [`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) provides backup
  guidance; deciding the operator's own RTO/RPO is the operator's call.
- **Regional natural disasters affecting operators.** A wildfire near
  an operator's data center is the operator's BCP problem. The project
  is not responsible for designing continuity arrangements for every
  geography an operator might deploy in.
- **Customer-side compliance program continuity.** If an operator
  using security-atlas has continuity needs inside their security
  program, that flows through the product surface (FrameworkScope,
  audit-period freezing), not this document.
- **24/7 uptime SLAs.** This is an OSS project; the project does not
  promise commercial availability targets. The targets in §2 are
  best-effort commitments calibrated to the solo-maintainer reality.

### What counts as a continuity event

A condition is a **continuity event** under this plan when it meets at
least one of:

1. A project property (repo, registry, docs site, SaaS instance) is
   unreachable or unusable for longer than the property's RTO target
   in §2.
2. Data loss of any data class enumerated in §5 has occurred or is
   strongly suspected.
3. The maintainer is unavailable for longer than two consecutive
   weeks during which release-blocking work was needed (this is the
   bus-factor trigger; see §7).
4. A security incident (per the [IR plan](./incident-response.md))
   crossed the line into requiring restore-from-backup procedures —
   typically ransomware, key compromise, or `main`-branch
   unauthorized modification.

Operational issues below these thresholds (a transient CI failure, a
docs site re-deploy required after a typo fix, a Dependabot bump) are
**not** continuity events. They flow through the normal slice / PR
pipeline.

---

## 2. RTO / RPO targets per tier

These targets bind the rest of the document. They are **honest, not
aspirational**. A solo-maintainer single-host deployment cannot achieve
SaaS-vendor uptime; the targets below name what the project commits to
today.

| Tier  | Description                                                                    | Examples                                                                                                                                             | **RTO**      | **RPO**            | Owner                            |
| ----- | ------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ | ------------------ | -------------------------------- |
| **0** | OSS continuity — the project's code and releases remain available indefinitely | GitHub repo (`mgoodric/security-atlas`), tagged releases on GitHub Releases, public license file, git history                                        | **24 hours** | **0 (no loss)**    | Project maintainer + GitHub      |
| **1** | Project distribution properties — operators can pull and verify artifacts      | Container registry (`ghcr.io/mgoodric/security-atlas`), release-asset downloads, the cosign signing chain (when slice 368 lands)                     | **7 days**   | **N/A**            | Project maintainer + GitHub      |
| **2** | Project communication properties — adopters can read docs and find guidance    | Docs site (GitHub Pages, mkdocs-material), the issue tracker, `CHANGELOG.md`                                                                         | **24 hours** | **0 (no loss)**    | Project maintainer + GitHub      |
| **3** | Maintainer-operated SaaS instance — single-host Unraid deployment              | The platform running for the maintainer's own use; Postgres state; evidence object storage; observability stack                                      | **4 hours**  | **24 hours**       | Project maintainer (sole)        |
| **4** | Maintainer-controlled supporting services — third-party integrations           | Codecov account state, GitGuardian account state, GitHub Actions runner availability, OIDC RP client secret, JWT signing keys, branch-protection PAT | **48 hours** | **N/A (rotation)** | Project maintainer + third party |

### Tier rationale

- **Tier 0 (code + git history) RTO 24h / RPO 0.** Git is distributed
  by design. Every clone is a full mirror; the maintainer keeps a
  local mirror; every contributor's clone is a recovery substrate.
  The 24h RTO reflects the time to identify a replacement hosting
  surface (Codeberg, Gitea, GitLab) and push the mirror; RPO 0
  reflects that no commit on `main` is ever uniquely held by GitHub.
- **Tier 1 (registry + release artifacts) RTO 7 days.** Container
  images are **reproducible from tagged commits**; if `ghcr.io` is
  lost, the recovery path is to push to a replacement registry. Seven
  days is the honest interval the maintainer commits to for that
  rebuild + DNS / docs / release-notes update path. RPO is N/A
  because the artifacts are deterministic outputs of the source —
  there is no "lost work" if the registry goes away, only "rebuild
  required."
- **Tier 2 (docs site) RTO 24h / RPO 0.** The docs site is generated
  from `main`; re-deploy is mechanical. RPO 0 reflects that content
  lives in the repo, not in any rendered cache.
- **Tier 3 (SaaS instance) RTO 4h / RPO 24h.** The maintainer-operated
  Unraid box is a single-host single-maintainer deployment. **4-hour
  RTO is best-effort during waking hours; longer outside.** It is not
  a 24/7 commitment. **24-hour RPO** matches the Postgres `pg_dump`
  nightly cadence documented in
  [`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) §"Backups"; tightening
  RPO below 24h would require introducing PostgreSQL WAL archival to
  the SaaS instance — that is named in §11 (Maintenance) as a future
  hardening item, not committed today.
- **Tier 4 (supporting services) RTO 48h.** Account state at Codecov
  / GitGuardian / GitHub Actions is largely externally-recovered (the
  vendors have their own SLAs). The 48h target reflects how long the
  maintainer would need to verify state post-recovery and rotate any
  credentials potentially exposed during the outage. RPO is "N/A —
  rotation" because the project can re-issue credentials but cannot
  recover state held inside vendor systems.

### What these targets are not

- **Not contractual.** No commercial agreement, no SLA refund, no
  uptime-credit machinery.
- **Not externally measured.** No public status page (deferred — see
  §9 and the IR plan's §6 "out of scope" content).
- **Not aspirational for a future SaaS offering.** When the
  GOVERNANCE.md re-evaluation trigger (2028-05-20 OR 100 deployed
  self-hosts) fires and Option B (hosted SaaS) is on the table, the
  Tier 3 targets are revisited. Until then, the SaaS instance is
  personal infrastructure with personal-infrastructure targets.

### What changes the targets

The targets above are revisited annually (see §11) and immediately
when:

- The advisory-council trigger from GOVERNANCE.md fires (≥ 3 outside
  contributors with ≥ 6 months sustained involvement). At that point
  the bus-factor improves; Tier 3 RTO can tighten.
- The GOVERNANCE.md re-evaluation trigger fires and the model
  evolves. A hosted SaaS offering would require Tier 3 / Tier 4 to
  graduate from personal-infrastructure targets to commercial ones.
- The maintainer materially upgrades the SaaS substrate (e.g., adds
  PostgreSQL WAL archival; moves to a multi-host deployment). The
  improvement updates the RPO targets at that slice's merge.

---

## 3. Roles and responsibilities

The standard BCP/DR roles are:

| Role                    | Responsibility                                                                                                                            |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| **BCP Coordinator**     | Owns the plan. Maintains the document, schedules annual tabletop, drives the post-event review after a real continuity event.             |
| **Recovery Lead**       | Executes the restore procedures in §6 during a real continuity event. Coordinates with the IR plan's Incident Commander when overlapping. |
| **Communications Lead** | Sends status updates to affected adopters (per §9 channels). Coordinates with the IR plan's Comms Lead when overlapping.                  |
| **Backup Custodian**    | Owns backup-system health. Confirms the nightly `pg_dump` ran; confirms parity is intact; confirms offsite replication is current.        |
| **Key Custodian**       | Owns the signing-key + credential rotation surface. Confirms keys are recoverable; rotates on schedule and on suspected compromise.       |

### Solo-maintainer role devolution

**The project currently operates with a single maintainer.** All five
roles above devolve to that maintainer. This mirrors the pattern
documented in the [IR plan §3](./incident-response.md#3-roles-and-responsibilities)
and the bus-factor reality documented in
[GOVERNANCE.md](../../GOVERNANCE.md) "Bus-factor & succession".

In practice this means:

- \*\*The maintainer is the BCP Coordinator + Recovery Lead + Comms Lead
  - Backup Custodian + Key Custodian\*\*, in parallel, during a real
    continuity event.
- **There is no second pair of eyes during the event.** The
  post-event review (§11) is the substitute — it surfaces
  restore-time decisions for retrospective scrutiny.
- **There is no formal escalation path during a real event.** If the
  maintainer is unavailable, see GOVERNANCE.md "Bus-factor &
  succession" — the project's escalation answer is the documented
  GitHub-org-transfer recovery path. The committed work to recruit a
  co-maintainer by 2027-05-20 (per GOVERNANCE.md) is the project's
  named path to reducing this risk.

### What devolution does not include

- **24/7 on-call rotation.** Not offered. Tier 3 RTO 4h is best-effort
  during waking hours.
- **Pager-equivalent automation.** Not committed. The maintainer
  notices the SaaS instance is down through ordinary use of the
  service; there is no separate alerting feed.
- **A dedicated DR site.** The Unraid box is single-host. The recovery
  procedure in §6 Scenario A is "swap hardware, restore from offsite
  backup" — there is no warm standby.

### When the role-stacking becomes untenable

Same trigger as the IR plan: the GOVERNANCE.md advisory-council
formation trigger (≥ 3 active outside contributors with ≥ 6 months
sustained involvement). When that trigger fires:

1. The maintainer designates a co-Recovery-Lead.
2. This section is updated to name the rotation.
3. The Tier 3 RTO is re-evaluated — tightening becomes feasible once
   the bus-factor is > 1.

Until then, single-person continuity is the honest answer.

---

## 4. Asset and dependency inventory cross-reference

**As of 2026-05-28 (slice 376), the canonical asset inventory for
the project lives at [`docs/governance/asset-inventory.md`](./asset-inventory.md).**
That document is the source of truth for the project's full asset
surface — code repositories, container images, cryptographic
material, deploy infrastructure, third-party integrations, and
documentation surfaces — with owner, criticality tier, location,
backup status, access surface, and lifecycle status per asset.

This section, which previously held a BCP working subset, is
superseded by the canonical inventory. The two documents bind as
follows:

- **Tier 1 assets** in `asset-inventory.md` §4 (project-stopping
  on loss/compromise) are the assets whose restore is documented
  in §6 of this BCP plan. Specifically, the SaaS instance's
  Postgres state, evidence object storage, evidence ledger, RLS
  policy set, and Unraid host (all Tier 1) map to BCP Scenarios
  A-D; the GitHub repository (Tier 1) maps to BCP Scenario E.
- **Tier 2 assets** in `asset-inventory.md` §4 (significant
  degradation on loss/compromise) include the container registry,
  observability stack, and supporting credentials. They map to
  BCP Scenarios A and E with lower restore priority.
- **Cryptographic material** inventoried at `asset-inventory.md`
  §3.3 includes the OAuth AS JWT signing keys, OIDC RP client
  secret, `BRANCH_PROTECTION_READ_TOKEN`, and the cosign signing
  key (when slice 368 lands). The recovery posture for each is
  rotation, governed by BCP §5 Tier 4 + §6 Scenario D / E.

When the asset inventory or this BCP plan changes, the two
documents are reconciled at the annual review (per §11) to keep
the cross-references valid.

---

## 5. Backup strategy

This section names the backup posture per load-bearing asset. Where a
backup is performed today, the cadence and verification are stated.
Where a backup is **intended** but not yet implemented, the gap is
explicitly named — per the engineer-as-collaborator process note in
this slice's work-order, the plan describes intended state and commits
to building any missing pieces, not lies about current state.

### Tier 0 — GitHub repository, releases, git history

| Question          | Answer                                                                                                                                                                                                                                               |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| What is backed up | The full git history (all branches, all tags), the full release-asset payload set, the issue tracker content, and any GitHub Discussions content                                                                                                     |
| How               | (a) GitHub itself is the primary store. (b) Maintainer-local mirror: `git clone --mirror` of `mgoodric/security-atlas` held on the maintainer's primary workstation                                                                                  |
| Frequency         | Maintainer-local mirror refresh: **weekly** (committed; quarterly governance-checkin records last refresh date)                                                                                                                                      |
| Retention         | Indefinite. Git history is small; mirror is kept in perpetuity                                                                                                                                                                                       |
| Encryption        | Maintainer-local mirror lives on encrypted disk (FileVault). Transport via SSH (GitHub) or HTTPS — TLS 1.2+ enforced by both sides                                                                                                                   |
| Integrity check   | `git fsck --full` runs on the mirror as part of the weekly refresh; corrupted-object output is a signal that fails the refresh                                                                                                                       |
| Gap noted         | An **off-GitHub mirror** (e.g., to Codeberg, a self-hosted Gitea, or a private GitLab) is **named as a hardening step** in §11. Not committed today. Tier 0 RPO 0 holds without it — the maintainer-local mirror is sufficient as recovery substrate |

### Tier 1 — Container registry, release artifacts, cosign chain (future)

| Question          | Answer                                                                                                                                                                                                                                                   |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| What is backed up | Container images at `ghcr.io/mgoodric/security-atlas` are **not** backed up as binary blobs. The **recovery posture** is rebuild-from-source: every tagged release reproduces deterministically from the tagged commit + Dockerfile                      |
| How               | Source recovery is via the Tier 0 mirror. Image rebuild is via the `release-please` + `docker build` workflow at `.github/workflows/release.yml`                                                                                                         |
| Frequency         | Not applicable — no scheduled binary backup                                                                                                                                                                                                              |
| Retention         | Per ghcr.io retention defaults; this project has not configured an override                                                                                                                                                                              |
| Encryption        | TLS in transit to/from ghcr.io. At-rest encryption is GitHub's responsibility                                                                                                                                                                            |
| Integrity check   | Per-image digest pinning in release notes (committed). cosign signature verification will land when slice 368 (cosign migration) ships — at which point operators can verify images independently of ghcr.io's posture                                   |
| Gap noted         | Image **rebuild requires the source tag to be intact**. If a git tag is lost AND the registry is lost simultaneously, recovery requires the maintainer-local mirror (Tier 0). This is the single point of failure that off-GitHub mirroring (§11) closes |

### Tier 2 — Docs site

| Question          | Answer                                                                                                             |
| ----------------- | ------------------------------------------------------------------------------------------------------------------ |
| What is backed up | Content lives in repo (`docs/`); rendered output is regenerable                                                    |
| How               | Inherent in Tier 0 backup                                                                                          |
| Frequency         | Inherent in Tier 0 backup                                                                                          |
| Retention         | Inherent in git history                                                                                            |
| Encryption        | Inherent in Tier 0 backup                                                                                          |
| Integrity check   | Inherent in Tier 0 backup. The `docs.yml` workflow is itself versioned; recovery is `git push` + `docs.yml` re-run |

### Tier 3 — SaaS instance (Postgres, object storage, Unraid host)

| Question          | Answer                                                                                                                                                                                                                                                                                                                           |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| What is backed up | (a) Postgres: nightly `pg_dump` per [`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) §"Backups". (b) Evidence object storage: S3-compatible bucket on the Unraid box. (c) Unraid host: parity disk + offsite replication to a secondary location                                                                                     |
| How               | Postgres: dump to local-host filesystem then `rclone` push to offsite S3-compatible store. Object storage: bucket-level versioning + lifecycle rules; cross-region replication on the offsite leg. Unraid: parity is real-time; offsite parity-aware backup is daily                                                             |
| Frequency         | Postgres: **nightly**. Object storage: continuous (versioning) + nightly replication. Unraid parity: real-time. Offsite replication: nightly                                                                                                                                                                                     |
| Retention         | Postgres dumps: 30-day rolling retention on offsite (older deleted by lifecycle rule). Object-storage versions: 30-day; lifecycle deletes older versions per `docs/SELF_HOSTING.md` guidance. Unraid offsite: 7-day rolling                                                                                                      |
| Encryption        | Postgres dumps: encrypted at rest in the offsite store (verified at slice 327 audit, finding section "verified-positive observations"). Object storage: bucket-level encryption at rest. Transport: TLS 1.2+ enforced                                                                                                            |
| Integrity check   | Postgres: nightly dump exits with a checksum recorded in the dump output; restoration tested annually during the §8 tabletop. Object-storage: sha256-per-evidence-record (canvas invariant #3 — the ledger is integrity-checked at every read). Unraid: parity check runs on the schedule the Unraid OS dictates                 |
| Gap noted         | **PostgreSQL WAL archival is not currently configured** on the SaaS instance — the RPO is 24 hours because that is what `pg_dump` nightly provides. Tightening to point-in-time recovery would require WAL archival; **named as a §11 hardening item, not committed today**. The Tier 3 RPO of 24 hours is honest about this gap |

### Tier 4 — Supporting services (signing keys, credentials)

| Question          | Answer                                                                                                                                                                                                                                                                                                                                                                                                           |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| What is backed up | Signing keys are not "backed up" in the traditional sense — the recovery posture is **rotate**. New keys are generated; old keys are revoked. The exception is the OIDC RP client secret, which has a single canonical value held in the maintainer's password manager + the SaaS instance's environment-variable surface                                                                                        |
| How               | Password manager has its own vendor-side backup posture (1Password or equivalent — the vendor choice is outside this document's scope per the [IR plan's communications-channel discipline](./incident-response.md#what-the-project-does-not-communicate) — naming the vendor would constitute exploit-roadmap detail). Maintainer-controlled offline copy held in encrypted form against vendor-side compromise |
| Frequency         | Rotation cadence is the substantive answer here: cosign key per slice 368 design (TBD); JWT signing keys per slice 366 (not yet scheduled — slice 327 audit M-1 finding); OIDC RP client secret on suspected compromise + at vendor-driven intervals; `BRANCH_PROTECTION_READ_TOKEN` per ADR-0005 default cadence                                                                                                |
| Retention         | Old keys retained for the post-rotation overlap window only (typically 7 days); then deleted                                                                                                                                                                                                                                                                                                                     |
| Encryption        | At-rest encryption in password manager + maintainer offline copy. Transport via TLS                                                                                                                                                                                                                                                                                                                              |
| Integrity check   | Rotation events are recorded in the unified audit log (per the [IR plan §4 detection inventory](./incident-response.md#what-is-monitored-the-what-to-detect-for-inventory)); annual review confirms the rotation log is complete                                                                                                                                                                                 |
| Gap noted         | **JWT signing key rotation is not currently implemented** (slice 327 audit M-1, tracked at slice 366). Until slice 366 lands, the recovery posture for a suspected JWT-key compromise is "rotate manually via the existing keystore-rewrite path documented in `internal/auth/keystore`" + force re-issue all active tokens. Documented here so the plan does not pretend the surface is automated               |

### What is explicitly not backed up

- **Maintainer workstation state** beyond the maintainer-local repo
  mirror. The maintainer's laptop is the maintainer's personal-IT
  concern, not a project asset.
- **Third-party-service account configurations.** Codecov,
  GitGuardian, GitHub Actions runner state are vendor-recovered.
- **Observability stack stateful data** on the SaaS instance.
  Re-instrumentation rebuilds the active dashboard; historical
  metrics / traces / logs older than the retention window are
  loss-acceptable.

---

## 6. Restore procedure per scenario

Each scenario below follows the standard **detect → contain → restore →
verify → resume operations** structure. The IR plan handles
**detect** and **contain** for incidents with a security dimension
(P0 / P1 in the IR plan's severity tiering); this plan handles
**restore → verify → resume**. The two plans interoperate explicitly
where the scenario has both an incident character and a continuity
character (Scenarios D and E especially).

### Scenario A — Unraid hardware failure on the SaaS instance

**Trigger.** A disk fails, the host reboots into a degraded state, or
a chassis-level failure makes the SaaS instance unavailable.

**Tier impacted.** 3.

**Recovery Lead.** Maintainer.

#### Detect

The maintainer notices the SaaS instance is unresponsive through
ordinary use, or via the Unraid OS's own health notifications. There
is no separate alerting feed.

#### Contain

1. Confirm the failure mode — disk vs CPU vs memory vs PSU — via the
   Unraid web UI (when reachable) or out-of-band console access.
2. If a disk failure: parity is intact by Unraid's design; the array
   continues running degraded.
3. If a chassis failure: the SaaS instance is down. The recovery is
   hardware swap.

#### Restore

1. **Disk failure path.** Replace the failed disk; Unraid initiates
   parity rebuild on the replacement disk. No further action while
   rebuild runs (typical duration: 8-24 hours depending on disk
   size). The service remains available throughout.
2. **Chassis failure path.**
   1. Swap the chassis to maintainer-owned spare hardware (the
      maintainer keeps spare hardware as part of the BCP commitment;
      named here so the commitment is visible).
   2. Re-mount the data disks on the spare chassis.
   3. Verify the Unraid OS recognizes the array.
   4. `docker compose -f deploy/docker/docker-compose.yml up -d` on
      the spare host.
   5. If data disks are also lost (worst case): restore from offsite
      backup per §5 Tier 3, then perform the docker-compose re-up.
3. Verify Postgres has come up cleanly: `pg_isready` returns OK;
   `atlas-cli health` returns OK (per slice 003 CLI contract).
4. Verify the object-storage bucket is reachable from the platform
   binary.

#### Verify

1. `atlas-cli evidence verify --tenant=<self>` returns no integrity
   failures across the recovered evidence ledger.
2. The unified audit log shows the recovery event as the first event
   post-restoration.
3. A test push via the Evidence SDK lands and is visible in the UI.
4. RLS context plumbing returns expected results: the maintainer's
   own tenant rows are visible; no cross-tenant leakage (this is the
   canvas invariant #6 verification).

#### Resume operations

1. Announce service restoration in the maintainer's personal status
   surface (currently informal; no public status page).
2. Append a one-line entry to the incident log at
   `docs/incidents/YYYY-MM-DD-saas-hardware-recovery.md` per the
   IR plan §10 template.
3. Conduct a post-event review per §11 below if RTO was missed.

**Documented gap.** **The maintainer's spare hardware commitment is
named here for the first time.** If spare hardware is not in fact
maintained, the chassis-failure recovery degrades to "acquire
hardware" (multi-day) + the above steps. The maintainer commits to
verifying spare-hardware availability at every annual review per §11.

### Scenario B — PostgreSQL corruption on the SaaS instance

**Trigger.** Postgres reports inconsistent data; a `pg_dump` fails;
the platform reports query failures with corruption-signaling error
codes.

**Tier impacted.** 3.

**Recovery Lead.** Maintainer.

#### Detect

Postgres logs surface the corruption. The platform may also surface
the corruption as 5xx responses (slice 367 generic-error helper
ensures the response is generic; the server-side log carries the
detail).

#### Contain

1. **Stop writes to the affected database** — bring the platform's
   atlas binary down via `docker compose stop atlas`.
2. Postgres continues running for read-only diagnostic access.
3. Snapshot the current Postgres data volume before any further
   action: `docker run --rm -v atlas-pg-data:/data -v
"$PWD":/backup busybox tar czf /backup/pre-restore-snapshot.tgz
/data` — this preserves the corruption-state for forensic review
   in case the restore is itself imperfect.

#### Restore

1. **Path 1 — recent corruption (within last 24h).** Restore from
   the most recent nightly `pg_dump` per §5 Tier 3.
   1. Pull the dump from the offsite store: `rclone copy
offsite:<bucket>/postgres-dumps/YYYY-MM-DD.sql.gz ./`.
   2. Verify the dump's checksum matches the recorded value.
   3. Drop the corrupted volume; create a fresh one.
   4. Restore: `gunzip < YYYY-MM-DD.sql.gz | psql -U atlas_migrate
-d security_atlas` (NOTE: restore uses `atlas_migrate` per the
      role-separation pattern at `migrations/bootstrap/01-roles.sql`).
   5. Re-grant `atlas_app` per the role-bootstrap script.
2. **Path 2 — older corruption (point-in-time recovery).** **Not
   currently supported** — PostgreSQL WAL archival is not configured
   on the SaaS instance (see §5 Tier 3 gap). Recovery degrades to
   Path 1 with up to 24h data loss.
3. Bring the platform back up: `docker compose up -d atlas`.

#### Verify

1. RLS policies are intact: `select count(*) from
information_schema.policies where schemaname = 'public'` returns
   the expected count.
2. `atlas-cli evidence verify` returns no integrity failures.
3. Per-tenant row counts are consistent with the dump's record.
4. The unified audit log shows the restoration event.

#### Resume operations

1. Announce service restoration.
2. Document the corruption cause in the incident log; file a slice
   for the root cause if it indicates a deeper issue.
3. If data loss occurred between the last dump and the corruption
   event, document the loss window in the incident log and
   coordinate with any affected tenant.

**Documented gap.** **24-hour RPO is the honest answer** because WAL
archival is not configured. Hardening to point-in-time recovery is in
§11.

### Scenario C — Object storage loss on the SaaS instance

**Trigger.** The S3-compatible bucket on the SaaS instance is lost
(disk failure on the storage tier; misconfigured lifecycle policy
that mass-deleted versions; bucket-level corruption).

**Tier impacted.** 3.

**Recovery Lead.** Maintainer.

#### Detect

The platform surfaces 5xx errors on evidence-retrieval paths. The
`atlas-cli evidence verify` command reports integrity failures
clustering on a specific bucket prefix or globally.

#### Contain

1. Stop the platform's atlas binary via `docker compose stop atlas`.
2. Confirm the loss scope — full bucket, prefix-scoped, or
   version-scoped (lifecycle misconfiguration).
3. If version-scoped: review the bucket's versioning history; the
   most recent versions may still be recoverable via undelete.

#### Restore

1. **Path 1 — versioning recovery (partial loss).** Use bucket-level
   versioning to restore deleted-but-not-purged versions. Document
   which versions were restored and which are lost.
2. **Path 2 — full-bucket loss (replay from evidence ledger).** This
   is the load-bearing recovery path that canvas invariant #3
   enables.
   1. The evidence ledger in Postgres carries the sha256 content
      hash of every evidence record (per slice 003's Evidence SDK
      proto + push contract).
   2. For each evidence record where the object-storage artifact is
      missing, mark the record as **artifact-lost** in the ledger.
      Do **not** delete the ledger entry — the append-only invariant
      forbids it.
   3. Re-ingest from upstream sources where possible. For
      AWS-connector evidence: the connector (slice 004) can be
      re-run; new evidence records are appended with new
      observed_at timestamps; the lost records remain marked
      artifact-lost in the ledger.
   4. For evidence that cannot be re-ingested (one-shot manual
      uploads, point-in-time observations), the artifact-lost mark
      is permanent; the audit-period freezing machinery (canvas
      §8.4) handles this by treating the missing artifact as
      "evidence existed at observation but artifact is now lost"
      rather than "evidence never existed."
3. Bring the platform back up: `docker compose up -d atlas`.

#### Verify

1. `atlas-cli evidence verify` reports remaining integrity failures
   on the artifact-lost records only.
2. Re-ingestion path: a test connector run completes and the new
   evidence is queryable.
3. The unified audit log records the artifact-lost markings as a
   single bulk event with the operator (maintainer) recorded.

#### Resume operations

1. Announce service restoration.
2. **If the loss affected an active audit period**, the
   audit-period-freeze machinery's sample-population guarantee
   degrades for that period. The IR plan post-incident-review
   process documents the impact; affected operators are notified
   per §9.
3. Document the loss in the incident log; file a slice for any
   adjacent hardening required.

**Documented load-bearing point.** Canvas invariant #3 (append-only
evidence ledger between ingestion and evaluation stages) is the
substrate that makes this scenario recoverable at all. Without the
ledger, full-bucket loss would be unrecoverable. This is why
invariant #3 is a constitutional commitment, not a design preference.

### Scenario D — Ransomware on the SaaS instance

**Trigger.** Files on the SaaS host are encrypted with attacker-held
keys; a ransom note appears; the platform is unreachable; the
maintainer detects unauthorized lateral movement on the host.

**Tier impacted.** 3.

**Recovery Lead.** Maintainer (also Incident Commander per IR plan §3
for this scenario — the IC and Recovery Lead are the same person, in
the same incident).

This scenario is **simultaneously an incident and a continuity
event**. The IR plan governs detection / containment / forensic
evidence preservation. This plan governs restore / verify / resume.
The two plans operate in parallel.

#### Detect

Per [IR plan §4](./incident-response.md#4-detection).

#### Contain

1. **Per IR plan §5.2** — disconnect the SaaS host from the
   network. Preserve forensic evidence per the IR plan.
2. Revoke any platform credentials that could have been exposed
   (OIDC RP client secret, JWT signing keys, any active tokens).
   Reissue per §6 Scenario E.
3. Assume the attacker has read-only access to all data on the host
   prior to the encryption event; treat all data as compromised
   until restore-verification proves otherwise.

#### Restore

1. **Do not pay the ransom.** Stated explicitly as the project's
   posture.
2. **Do not restore in-place** on the compromised host. Restore to a
   fresh chassis (per §6 Scenario A path 2 — chassis swap).
3. **Restore from offsite backup**, not from the local host
   filesystem — local files are potentially attacker-modified.
   Specifically:
   1. Postgres: restore from the most recent offsite `pg_dump` per
      §6 Scenario B.
   2. Object storage: restore from the offsite bucket replication
      per §6 Scenario C.
   3. Unraid host: rebuild from a known-good Unraid configuration;
      restore data disks from offsite parity-aware backup.
4. **Assess the audit-period freeze integrity** per canvas §8.4.
   Any audit period whose `frozen_at` predates the
   suspected-compromise window is unaffected. Any period whose
   `frozen_at` falls within the compromise window must be
   re-frozen against the restored evidence ledger; the IR plan
   post-incident review documents any operator-visible impact.
5. **Verify the slice 327 audit's verified-positive controls are
   intact** in the restored environment:
   - Encryption at rest enabled.
   - No `InsecureSkipVerify` in the binary's TLS config.
   - RLS policies present.
   - Argon2id parameters at the documented baseline.

#### Verify

1. Full integrity-verify pass: `atlas-cli evidence verify`.
2. Forensic verification: compare the restored binary's hash against
   the published cosign signature (when slice 368 lands; meanwhile,
   the maintainer's locally-cached known-good binary hash).
3. Active token re-issue confirmed: all pre-incident JWTs reject.
4. Audit log shows a clean post-restoration event sequence.

#### Resume operations

1. Per IR plan §6 — communications. **Email to known operators is
   reserved for P0 with active exploitation** per the IR plan; this
   scenario qualifies if any operator-affecting artifact (release,
   container image) was modified during the compromise window.
2. CHANGELOG `### Security` entry per IR plan.
3. CVE assignment if applicable.
4. Conduct the post-incident review per IR plan §9 AND the
   post-event review per §11 below.

**Documented load-bearing point.** This scenario is the worst-case
continuity event the project plans for. The composability of the IR
plan (detect / contain / communicate) and this plan (restore /
verify / resume) is the project's defense-in-depth posture for
operator-affecting incidents.

### Scenario E — GitHub organization compromise

**Trigger.** The `mgoodric` GitHub account is compromised, the
`mgoodric/security-atlas` repo is modified by an unauthorized party,
or GitHub itself loses the repository (account suspension,
infrastructure incident, account-recovery failure).

**Tier impacted.** 0 (catastrophic, project-wide).

**Recovery Lead.** Maintainer.

This scenario is also simultaneously an incident (per IR plan §7.2 —
auth compromise) and a continuity event. The IR plan's auth-compromise
playbook drives containment; this plan drives restoration of project
continuity.

#### Detect

Per IR plan §7.2. Additional signals specific to this scenario:
unexpected commits on `main`; release tags created by parties other
than the maintainer; the GitHub repo unexpectedly inaccessible.

#### Contain

1. **Per IR plan §7.2** — revoke every PAT, disable OAuth grants,
   force-revoke active sessions.
2. If the maintainer's GitHub account is recoverable: change
   password, enable hardware-token 2FA if not already, audit
   account-recovery options.
3. If the GitHub account is not recoverable (full account loss):
   proceed to "GitHub-loss recovery path" below.

#### Restore

1. **Path 1 — account recoverable.** Per IR plan §7.2 eradication +
   recovery. Audit `main` against the maintainer's local mirror
   `git log` to identify unauthorized commits; revert as needed.
   Re-sign or re-tag releases that were modified.
2. **Path 2 — full GitHub-loss recovery.**
   1. Identify a replacement hosting surface (Codeberg, a
      self-hosted Gitea instance, GitLab). The maintainer's choice
      is documented in the same slice that performs the migration.
   2. Push the maintainer-local mirror to the replacement: `git
remote add new-origin <url> && git push --mirror new-origin`.
   3. Update `docs/` cross-references to point at the new
      canonical URL.
   4. Update `package.json`, `go.mod`, and any other in-repo
      reference to the canonical GitHub URL.
   5. Publish a `SECURITY-INCIDENTS.md` notice (per IR plan §6)
      documenting the migration and the new canonical location.
   6. Update any third-party services (Codecov, GitGuardian) to
      point at the new repository surface.
3. **Rotate all keys** per §6 Scenario E's IR plan §7.2 directives,
   slice 366 when shipped (JWT key rotation), and the
   §5 Tier 4 rotation procedures. **Treat every project-controlled
   credential as compromised** during the incident window.
4. **Audit trail review** per IR plan §8 — the unified audit log
   from the maintainer's local SaaS instance is the post-compromise
   source of truth for which events did and did not occur. Cross-
   check against any externally-cached state (e.g., issue replies
   received by email).

#### Verify

1. Repository integrity: the recovered repo's HEAD matches the
   maintainer-local mirror's HEAD modulo the unauthorized commits.
2. cosign verification (when slice 368 lands): all release artifacts
   are signed by the maintainer's key, not an attacker's.
3. CI pipelines run cleanly on the new hosting surface.
4. Operators can pull from the new registry path; the docs site
   re-renders.

#### Resume operations

1. Public statement per IR plan §6 — `SECURITY-INCIDENTS.md`.
2. CVE assignment if any operator-affecting artifact was
   compromised.
3. The post-incident review per IR plan §9 explicitly addresses the
   "why was account-recovery 2FA insufficient?" question.
4. The post-event review per §11 documents whether off-GitHub
   mirroring (currently a §11 hardening item) would have shortened
   recovery time; this scenario is the strongest case for promoting
   off-GitHub mirroring from "hardening item" to "committed work".

**Documented load-bearing point.** **The maintainer-local mirror is
the load-bearing recovery substrate** for full GitHub loss. Tier 0
RPO 0 holds because of that mirror. If the mirror were lost
simultaneously with GitHub (e.g., maintainer-workstation
encryption failure during a GitHub outage), Tier 0 RTO degrades and
the project's continuity posture has a real hole. **Off-GitHub
mirroring (§11) is the named mitigation for this single-substrate
risk.**

---

## 7. Continuity of the OSS project

The scenarios in §6 assume the maintainer is available to execute
the recovery. This section addresses what happens when **the
maintainer is unavailable** for an extended period — illness,
incapacitation, or prolonged absence.

This section is the operational complement to
[GOVERNANCE.md](../../GOVERNANCE.md) "Bus-factor & succession", which
states the bus-factor problem plainly. This document does not modify
GOVERNANCE.md (per P0-373-5 in the slice work-order); it
operationalizes the recovery sequence GOVERNANCE.md names.

### What is committed

- **Bus-factor: 1.** The project currently has one maintainer. This
  is the load-bearing fact this section responds to.
- **Apache 2.0 license ensures fork-ability as ultimate fallback.**
  Per GOVERNANCE.md, "the project's Apache 2.0 license and the
  immutability of git history ensure the code remains available
  forever." Downstream forks can continue development independently
  of the project's primary repo. This is not a workflow — it is the
  ultimate guarantee.
- **Maintainer commits to recruiting a co-maintainer by
  2027-05-20** per GOVERNANCE.md "Bus-factor & succession". This BCP
  document reproduces the commitment as the named risk-reduction
  path.

### What is named but not currently committed

- **The maintainer commits in GOVERNANCE.md to documenting the
  GitHub-org-transfer recovery path in a sealed envelope held with a
  personal trusted contact** for the bus-factor scenario. This BCP
  document does not duplicate the envelope's content (per P0-373-2 —
  no exploit-roadmap detail). The envelope is the project's
  pre-arranged successor surface; until the envelope exists in
  practice, the bus-factor scenario degrades to "fork from any
  contributor's local clone."
- **Advisory council formation per GOVERNANCE.md trigger.** Once the
  ≥ 3 outside contributors with ≥ 6 months sustained involvement
  trigger fires, this section is updated to name the council's
  recovery role.

### Extended maintainer absence — operational steps

This sequence describes what happens if the maintainer is unavailable
for a defined period. It is intentionally short because the
single-point-of-failure cannot be hedged with workflow alone.

1. **Weeks 1-2.** Project repository remains open; CI continues
   running on push (no human intervention needed for the runners).
   No releases ship; no PR reviews happen. Issues accumulate.
2. **Week 3.** Trusted contact (per GOVERNANCE.md sealed-envelope
   commitment, when established) opens an issue on the repository
   noting the maintainer's absence and the expected return window if
   known.
3. **Months 2-3.** If the maintainer remains unavailable, the
   trusted contact follows the GitHub-org-transfer recovery path
   from the sealed envelope. If no transfer path is established,
   downstream forks (the Apache-2.0 fork-ability fallback) become
   the project's continuation surface.

### What contributors can do during maintainer absence

- **Fork.** The license explicitly allows it. Forks can ship
  releases under their own org names; the canonical hosted URL
  remains `mgoodric/security-atlas` until the org transfer happens.
- **Continue running their own deployments.** The shipped binary
  does not phone home; the deployment is unaffected by repository
  activity (or lack thereof).
- **Document publicly that a fork exists** — typically by opening a
  GitHub issue or by updating their own README. This is a courtesy,
  not a requirement.

### Re-evaluation when bus-factor improves

When the GOVERNANCE.md advisory-council trigger fires (≥ 3 outside
contributors with ≥ 6 months sustained involvement), this section is
revised to name the council's role in maintainer-unavailable recovery.
The advisory council, if formed, becomes the named successor surface
in place of the sealed-envelope mechanism. The transition happens at
the same slice that formalizes the advisory council in GOVERNANCE.md.

---

## 8. Testing the plan

The plan is tested by **annual tabletop exercises**, supplemented by
the chaos-experiment backlog and the audit cycle.

### Tabletop exercises

**Cadence: annual, co-scheduled with the
[IR plan](./incident-response.md) tabletop.** The first tabletop is
due **2027-05-28** — one year from this document's filing date and
the same date as the IR plan's first tabletop. Co-scheduling is
deliberate: the recovery scenarios in §6 cross-reference IR plan
playbooks; exercising both together surfaces composition issues that
single-plan tabletops would miss.

Tabletop scope rotates between scenarios:

- **Year 1 (2027-05-28):** Scenario A (Unraid hardware failure) +
  Scenario B (Postgres corruption). These are the highest-
  probability scenarios and cover the most-used restore paths.
- **Year 2 (2028-05-28):** Scenario C (object storage loss) +
  Scenario E (GitHub org compromise — Path 1, account-recoverable).
  Higher-impact, lower-probability scenarios.
- **Year 3 (2029-05-28):** Scenario D (ransomware) +
  Scenario E Path 2 (full GitHub-loss). Worst-case scenarios; the
  GOVERNANCE.md re-evaluation trigger is expected to fire within
  this window, which informs the conversation.
- **Year 4+:** Cycle repeats with rotation tuned to incident
  history.

Tabletop output is recorded at
`docs/audit-log/tabletop-YYYY-MM-DD.md` (shared file with the IR
plan tabletop) and:

- Surfaces any restore step that proved unactionable.
- Files slices to address gaps.
- Updates this document inline where the procedure is wrong.

### Chaos experiments as the operational test substrate

The [slice 335 chaos-experiment design backlog](../audits/335-chaos-experiment-design.md)
defines eight chaos experiments. Of those, two directly exercise
this plan's restore paths:

- **Experiment 5 (Postgres failover)** stress-tests Scenario B.
- **Experiment 7 (object-storage outage)** stress-tests Scenario C.

When the chaos backlog executes (v2+ slices 354-358), these
experiments serve as **operational tests** of the restore machinery
— the experiment runs in a controlled environment with documented
abort criteria, but the playbook executed is the playbook this
document defines. Falsified hypotheses become incidents per the IR
plan and are worked through this plan's recovery procedures.

### Audit cycle as passive testing

The quarterly security and compliance audit cadence (slice 327 + 329

- similar future audits) passively tests this plan: an audit finding
  that surfaces a missing backup / unverified restore / unrotated key
  is a continuity-event predecessor. The H-2 finding from slice 329 is
  itself the source of this document.

If the audit cycle stops surfacing continuity-related findings, that
is itself a signal worth investigating.

---

## 9. Communication during continuity events

The communications playbook for continuity events is **intentionally
narrow and reuses the IR plan's playbook** where applicable. The
project has no PR function, no marketing team, no spokesperson
rotation. What it has is a documented set of channels with clear
triggers.

| Channel                                | When to use                                                                                                                                                                                                         | Owner                   |
| -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| **GitHub issue / discussion**          | Default channel for any continuity event affecting Tier 0-2 properties. A user reporting "I can't pull the image" opens an issue; the maintainer responds with status.                                              | Maintainer (Comms Lead) |
| **`CHANGELOG.md`**                     | Continuity events that resulted in a public-facing impact (yanked release, replaced image tag, repo migration). Documented as a `### Fixed` or `### Security` entry depending on incident character.                | release-please workflow |
| **`SECURITY-INCIDENTS.md`**            | Reserved for reputation-affecting events per IR plan §6 — full GitHub-loss recovery, ransomware, key compromise. Created on first use.                                                                              | Maintainer              |
| **GitHub Security Advisory**           | When a continuity event also satisfies the IR plan's P0/P1 advisory threshold. Filed alongside the recovery work; published on coordinated-disclosure date.                                                         | Maintainer              |
| **Email to security@ contact**         | The `SECURITY.md` security@ address is reachable independent of the SaaS instance. Adopters reaching out about a SaaS-instance outage receive a response when the maintainer becomes available.                     | Maintainer              |
| **Direct outreach to known operators** | Reserved for ransomware / key-compromise scenarios where operator-affecting artifacts may have been altered. The project does not maintain an operator mailing list; reach happens through whatever contacts exist. | Maintainer              |

### Channels explicitly not committed

- **Public status page.** Considered and explicitly deferred —
  consistent with the IR plan §6 "out of scope" decision. Operating
  a status page requires the status page to be hosted _somewhere
  other than the failing infrastructure_. The project would need to
  add an external hosted dependency (e.g., a Statuspage.io account
  or equivalent) for the status page to be useful during a Tier 0-2
  outage. This is a hardening item in §11; not committed today.
- **Social media announcements.** No official project social
  account. The maintainer has personal accounts that are explicitly
  outside the project's scope per GOVERNANCE.md "Funding posture"
  ("The maintainer's separate consulting, conference-speaking, and
  writing income is entirely outside the project's scope").
- **Phone or in-person comms.** Not offered. The maintainer responds
  in writing through the documented channels.

### Tone discipline

Continuity-event communications follow the same tone discipline as
the IR plan §6 — measured, factual, no marketing voice, no
unprompted superlatives, no banned phrases per
[`board-narrative-tone-anti-patterns.md`](./board-narrative-tone-anti-patterns.md).
"The container registry has been temporarily unavailable since
14:00 UTC. Restoration is in progress; expected completion within
the documented 7-day RTO." Not "we are working tirelessly to
leverage industry-leading recovery techniques to restore world-class
availability."

---

## 10. Documentation and audit trail

Every continuity event is documented in two artifacts:

1. **The incident log** at `docs/incidents/YYYY-MM-DD-<slug>.md`
   per the [IR plan §10 template](./incident-response.md#10-incident-log-template).
   Continuity events use the same log shape as security incidents.
   The log captures detect / contain / restore / verify / resume
   timestamps and decisions.
2. **`CHANGELOG.md`** — for continuity events that resulted in a
   public-facing impact, the changelog entry is the public-facing
   audit trail. `### Fixed` for operational restorations; `###
Security` if the event had a security character.

### File naming

- Continuity-event logs share the IR plan's directory:
  `docs/incidents/YYYY-MM-DD-<slug>.md`. The slug describes the
  event — `2026-07-15-postgres-corruption-restore`,
  `2027-02-03-ghcr-registry-rebuild`, etc.
- A small number of continuity events per year is the realistic
  baseline. If the cadence exceeds 6 per year (excluding tabletop
  exercises), that is a signal worth investigating.

### Confidentiality

Same posture as the IR plan §8: incident logs are **public by
default**. Where attack-vector detail must be redacted (specifically
for Scenarios D and E), the public log carries a
`[redacted — see private archive]` placeholder. The unredacted
material is held by the maintainer privately.

### Cross-references in the incident log

Every continuity-event log links:

- The scenario in §6 that the recovery followed.
- The backup artifact used (offsite Postgres dump filename + hash;
  object-storage version timestamp; etc.).
- Any IR plan playbook invoked in parallel.
- The CHANGELOG entry, if one was published.
- The post-event review, if one was conducted.

---

## 11. Maintenance

### Review cadence

This document is reviewed **annually** by the maintainer, co-scheduled
with the IR plan tabletop. The next review is due **2027-05-28**.

The annual review surfaces:

- RTO/RPO targets that proved unachievable in practice during the
  past year's real events or tabletop exercises.
- Restore procedures that did not match real-world execution.
- Asset inventory drift (when slice 376 lands, this section
  cross-references the canonical inventory; until then, §4's table
  is reviewed for additions or retirements).
- Channels added or retired (operator mailing list, status page,
  off-GitHub mirror) since the last review.
- Bus-factor state per GOVERNANCE.md quarterly checkin trend data.
- Role devolution updates if the advisory-council trigger has fired.
- Tabletop and chaos-experiment outcomes since the last review.

The review's output is a PR that updates this file plus an annual
review note at `docs/audit-log/business-continuity-review-YYYY.md`
(shared documentation pattern with the IR plan's annual review).

### Ownership

The project maintainer owns this document. Changes follow the
standard slice / PR / DCO process documented in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md).

### Relationship to ISO 27001 5.36

ISO 27001 5.36 ("Monitoring, review and change management of
information security") expects governance policies to be reviewed on
a fixed cadence with documented results. The annual review cadence
above is the project's commitment to that clause for the BCP/DR
surface, mirroring the IR plan's matching commitment. Per the slice
329 audit report §9, this commitment is recorded as a capability,
not as a certification claim.

### Named hardening items (not committed today)

The following items would materially improve the project's
continuity posture. They are **named here for visibility** so the
maintainer's annual review surfaces them for prioritization. Each
is named with the gap it closes:

| Item                                         | Gap it closes                                                                                                                          | Status                                                                                                 |
| -------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| **Off-GitHub repository mirror**             | Single-substrate risk in Tier 0 — maintainer-local mirror is the only fallback; if lost concurrently with GitHub, recovery is degraded | Named; not committed. Triggered if Scenario E Path 2 ever fires, or at the maintainer's discretion     |
| **PostgreSQL WAL archival on SaaS instance** | 24-hour RPO on Tier 3 — WAL archival enables point-in-time recovery, tightening RPO toward minutes                                     | Named; not committed. RPO 24h holds without it                                                         |
| **JWT signing key rotation automation**      | Slice 327 audit M-1 finding — currently rotation is manual                                                                             | **Tracked at slice 366** (committed work; not yet scheduled). When 366 lands, the §5 Tier 4 gap closes |
| **cosign image signing**                     | Operators cannot verify ghcr.io images independently of GitHub                                                                         | **Tracked at slice 368** (committed work; not yet scheduled). When 368 lands, Tier 1 hardens           |
| **Public status page**                       | No external-to-failing-infrastructure surface for status announcements during Tier 0-2 outages                                         | Named; not committed. Considered in IR plan §6 as deferred; revisited here at annual review            |
| **Operator mailing list**                    | No way to reach adopters directly during ransomware / key-compromise scenarios                                                         | Named; not committed. Considered in IR plan §6 as deferred; revisited here at annual review            |
| **Spare hardware verification**              | Scenario A chassis-failure path assumes maintainer-owned spare hardware exists                                                         | **Named; verified at every annual review.** The first verification is at the 2027-05-28 review         |

### When to deviate from this plan

This plan describes the default response. The Recovery Lead may
deviate when an event's shape demands it — for example, a continuity
event whose recovery path requires coordination with a third party
whose timing constraints override the project's normal restore
cadence. Deviations are documented in the incident log with a
one-line rationale, per the IR plan §11 pattern.

---

## Document history

| Date       | Change                  | Slice |
| ---------- | ----------------------- | ----- |
| 2026-05-28 | Initial document filed. | 373   |
