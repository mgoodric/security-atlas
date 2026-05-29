# Access review cadence

**Status:** Active governance document.
**Filed:** 2026-05-28 by slice 374.
**Closes:** Slice 329 compliance meta-audit finding **H-3** (no documented GitHub access review cadence).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual, co-scheduled with the [incident-response plan](./incident-response.md) and [business-continuity plan](./business-continuity.md) tabletop. Next review: 2027-05-28.

---

## Why this document exists

The platform's **technical** access controls are excellent. OIDC RP
authenticates humans through an external IdP; the internal OAuth
Authorization Server mints JWTs carrying `atlas:current_tenant_id` and
`atlas:available_tenants[]`; PostgreSQL Row-Level Security enforces
tenant isolation at the database layer (canvas invariant #6); RBAC +
ABAC via OPA evaluate authorization decisions. The slice 327 security
audit verified these controls are configured correctly.

What the project did not have before this document: a **periodic
review** of who has access to the GitHub repository, the CI pipeline,
the third-party integrations, the signing material, and the
maintainer-operated SaaS instance's administrative surface. Slice 329's
compliance meta-audit identified this gap as **H-3** — High severity,
load-bearing for the "do you review access at least annually?" question
that every third-party diligence reviewer asks first.

SOC 2 CC6.2 + CC6.3 + CC6.5 expect a documented periodic review of
logical access. ISO 27001 5.18 (Access rights) and 8.2 (Privileged
access rights) expect the same. NIST CSF PR.AC-1 (Identities and
credentials are issued, managed, verified, revoked, and audited) is
the same control mapped to a different framework. All three expect a
**cadence** plus a **procedure** plus an **evidence trail**.

This document fills that gap. It is short by design — the maintainer
must be able to read it once a quarter, walk through the procedure,
and produce the evidence artifact without consulting other documents.

This document **describes capabilities**, not certifications.
security-atlas is not SOC 2 certified, not ISO 27001 certified, not
HIPAA-attested. The cadence below documents what the project commits
to today; it does not claim third-party attestation of that
commitment.

Cross-references:

- [`docs/governance/incident-response.md`](./incident-response.md) — the response side of access compromise. An access-review finding that surfaces an unauthorized grant becomes an incident at minimum P2 severity (per IR plan §2 reclassification).
- [`docs/governance/business-continuity.md`](./business-continuity.md) — §5 Tier 4 (supporting services and signing keys) names the credentials this document reviews on cadence; §6 Scenario E (GitHub org compromise) is the response-path when an access review surfaces post-hoc evidence of compromise.
- [`GOVERNANCE.md`](../../GOVERNANCE.md) — bus-factor & succession. Section 5 of this document operationalizes the access-review implications when the maintainer is unavailable for extended periods.
- [`SECURITY.md`](../../SECURITY.md) — disclosure-triggered out-of-band reviews are named in §7 (Trigger-based reviews); coordination with the IR plan is required when a disclosure implicates access.
- [`docs/adr/0005-branch-protection-pat-vs-app.md`](../adr/0005-branch-protection-pat-vs-app.md) — names the `BRANCH_PROTECTION_READ_TOKEN` PAT this document reviews annually.
- [`Plans/canvas/06-risk.md`](../../Plans/canvas/06-risk.md) and [`Plans/canvas/01-vision.md`](../../Plans/canvas/01-vision.md) §6 — the v1 binary success criterion ("survive a third-party security review") that this document is load-bearing for.
- [`docs/audits/327-security-audit-security-auditor-report.md`](../audits/327-security-audit-security-auditor-report.md) — the verified-positive technical controls that this document complements at the organizational layer.
- [`docs/audits/329-compliance-meta-audit-report.md`](../audits/329-compliance-meta-audit-report.md) — the audit finding H-3 that filed this slice.

---

## 1. Purpose and scope

### What "access review" means here

An **access review** under this plan is a maintainer-driven periodic
walk-through of every access grant the project currently holds, with
a yes/no decision recorded for each: **continue, revoke, or
reduce-scope**. The walk-through is procedural; it produces a
timestamped artifact under `docs/governance/access-reviews/`; it
files a `### Security` CHANGELOG entry if any grant is revoked or
reduced.

The review is **not** a continuous-monitoring loop. The platform
already implements continuous controls via Dependabot, CodeQL,
govulncheck, Trivy, GitGuardian, and the unified audit log. The
access review is the **organizational supplement** — the periodic
human-in-the-loop check that the technical controls cannot perform
on themselves: deciding whether a grant is still needed, whether
its scope is still minimal, whether the integration's continued
authorization is still warranted.

### What this plan covers

The access-review surface for **the security-atlas project itself**:

- The `mgoodric/security-atlas` GitHub repository's access grants:
  owner, collaborators, branch-protection rule administration.
- GitHub Apps installed against the repository (Dependabot, Codecov,
  GitGuardian, release-please, StepSecurity Harden-Runner, any others
  installed since this document's last review).
- OAuth Apps authorized to act on behalf of the maintainer's GitHub
  account in any capacity that touches the repository.
- CI secret store at `.github/workflows/`'s `secrets.*` references —
  every secret named in any workflow file.
- Container registry `ghcr.io/mgoodric/security-atlas` push tokens
  and read tokens.
- Webhook receivers configured against the repository (deploy hooks,
  notification hooks, third-party integration receivers).
- Deploy keys configured against the repository.
- The maintainer's GitHub Personal Access Tokens that have any scope
  touching the repository (`repo`, `workflow`, `read:packages`,
  `write:packages`, `admin:org` — the latter is not currently used
  but is included here in case it is added).
- Signing keys used by the project: the maintainer's GPG signing key
  (DCO sign-off + release-tag signatures); the cosign signing key
  (when slice 368 lands).

### What this plan does not cover

- **Per-PR review.** Every PR's content is reviewed via the standard
  branch-protection ruleset (required reviewer + required status
  checks + DCO sign-off). That is a separate discipline, not an
  access review. This plan reviews who has the **authority** to
  approve a PR, not the content of any specific PR.
- **Inside operator-hosted deployments.** Operators self-hosting
  security-atlas review their own access grants per their own
  programs. This document is the project's own plan; it is not a
  template the project commits to maintaining for operators (though
  operators are welcome to adapt it).
- **Platform tenant access reviews.** The platform's customer-facing
  access-review machinery (per canvas §4.6 if/when access-review
  becomes a first-class evidence kind) is product surface, not
  project-self-governance surface. The two share vocabulary; they
  are separate concerns.
- **Identity providers used by the maintainer personally.** The
  maintainer's Google account, password manager vendor, hardware
  token vendor, and any other personal-IT identity surfaces are
  out of scope for this document. They are referenced where
  load-bearing (e.g., GitHub two-factor authentication) but the
  vendor-side review of those accounts is the maintainer's
  personal-IT concern.

### Repository ownership note (engineer-as-collaborator)

The work-order that spawned this document refers to "GitHub org" access
review cadence. In practice, `mgoodric/security-atlas` is owned by an
**individual GitHub account** (`mgoodric`), not by a GitHub
organization. The access-review surface is therefore:

- The owning user account's permissions (full admin by definition).
- Per-repository collaborator grants (currently: only the maintainer).
- Per-repository third-party App and OAuth grants.

The user-owned shape is not a deficiency for an early-stage OSS
project; it reflects the bus-factor reality named honestly in
[GOVERNANCE.md](../../GOVERNANCE.md). Transitioning the repository
to a GitHub organization is a possibility that is named but **not
committed today**; see §9's hardening-items table. The cadence
defined below applies equally whether the repository remains
user-owned or migrates to an organization — only the specific
`gh api` commands in §4 change.

---

## 2. Inventory of access surfaces

This section enumerates the access-surface categories the project
holds today, at the resolution that informs the cadence in §3.
**Specific grants are not enumerated here** — per the slice 374
threat-model analysis (P0-374-2), publishing a current grant list in
the cadence document tells an attacker what to target. Specific
grants are recorded in the per-review evidence artifacts under
`docs/governance/access-reviews/YYYY-QQ.md`.

### 2.1 Repository ownership and collaborators

The `mgoodric/security-atlas` repository is currently owned by a
single GitHub user account (`mgoodric`). The user account is the
sole administrator with full repository control by definition.

Per-repository collaborator grants are configurable in five
GitHub-standard roles: **read**, **triage**, **write**, **maintain**,
**admin**. As of this document's filing, the only collaborator with
any role beyond what the public-OSS license grants is the maintainer
themselves (verified via `gh api repos/mgoodric/security-atlas/collaborators`
at filing time; specific output is held in the first per-review
evidence artifact rather than inlined here).

The branch-protection ruleset is documented at
[`.github/branch-protection.json`](../../.github/branch-protection.json)
per [ADR-0005](../adr/0005-branch-protection-pat-vs-app.md). The
ruleset itself is part of this access-review surface — who has the
authority to modify it is a privileged-access question that the
annual review addresses.

### 2.2 GitHub Apps installed

GitHub Apps installed against the repository provide automated
functions: dependency scanning, code coverage, secret scanning,
release automation, CI hardening. The apps installed at filing
time (categorical list; specific app IDs in the per-review
evidence artifact):

- **Dependency scanning.** Dependabot.
- **Code coverage upload.** Codecov.
- **Secret scanning.** GitGuardian.
- **Release automation.** release-please.
- **CI hardening.** StepSecurity Harden-Runner.
- **CodeQL.** GitHub-native; reviewed for permission scope rather
  than installation status.

Each installed App has a permission scope — the GitHub permissions
the App requested when installed. The semi-annual review (per §3)
walks each App's current scope and confirms it is still minimal.

### 2.3 OAuth Apps authorized

OAuth Apps authorized to act on behalf of the maintainer's GitHub
account in any capacity that touches the repository are part of
this surface. The maintainer reviews `https://github.com/settings/applications`
during the semi-annual review and confirms each authorization is
still warranted.

### 2.4 CI secret store

The CI secret store is the set of `secrets.*` references in
`.github/workflows/*.yml`. Categorically (specific names withheld
per P0-374-6; full list in the per-review evidence artifact):

- A branch-protection read-only PAT — documented in
  [ADR-0005](../adr/0005-branch-protection-pat-vs-app.md).
- A Codecov upload token — issued by the Codecov platform.
- A release-please App private key — for release automation.
- A Homebrew tap publishing token — for the Homebrew formula
  release.
- A CI test-database password — bootstrap password for the
  integration-test Postgres instance.
- GitHub-native `GITHUB_TOKEN` (no rotation required; refreshed
  per-workflow-run by GitHub).

Each CI secret has a rotation surface — where the secret is
originally issued, what its rotation cadence is, who would notice
if it were silently invalidated. The quarterly review (per §3)
walks each CI secret and verifies it is still needed.

### 2.5 Container registry tokens

The `ghcr.io/mgoodric/security-atlas` registry is the public
container-image distribution surface for the project. Push tokens
are issued by GitHub's package machinery and are scoped per workflow.
Read access is anonymous for public repos and does not require
review.

### 2.6 Webhook receivers and deploy keys

Webhook receivers configured against the repository are
event-notification surfaces; deploy keys are SSH-key surfaces granted
to specific deployment hosts. The annual review (per §3) walks each
configured webhook and each deploy key and confirms it is still
needed.

### 2.7 Personal Access Tokens

The maintainer's GitHub Personal Access Tokens that touch the
repository are part of this surface. PATs are reviewed against the
GitHub Settings → Personal access tokens page during the annual
review; each PAT's scope is confirmed minimal and each token's
continued existence is confirmed warranted.

The `BRANCH_PROTECTION_READ_TOKEN` documented by ADR-0005 is one
such PAT. Others may be created over time; the annual review is
the cadence at which their inventory is reconciled.

### 2.8 Signing keys

Two signing-key surfaces are in scope:

1. **The maintainer's GPG signing key** — used for DCO sign-off on
   commits and for release-tag signatures. Annual review confirms
   the key is still under maintainer control and has not been
   compromised; rotation is performed if signs of compromise
   surface (per the IR plan §7.2 auth-compromise playbook).
2. **The cosign signing key** — when slice 368 lands, the cosign
   key joins this list. Annual review confirms the cosign key is
   still under maintainer control. The rotation cadence for cosign
   will be defined as part of slice 368's design.

JWT signing keys used by the platform's OAuth Authorization Server
(per [ADR-0003](../adr/0003-oauth-authorization-server.md)) are
**not** part of this document's review surface — they belong to
the operator-side cryptographic key management policy, which is
tracked at slice 366 (JWT key rotation automation per slice 327
audit M-1). This document reviews **project-self-governance** keys
(GPG, cosign); slice 366 will define the rotation cadence for the
runtime JWT keys.

---

## 3. Review cadence per tier

The cadence below is calibrated to **what a solo maintainer can
sustain**. Each tier names a frequency, the access surfaces it
covers, and the rationale.

| Tier            | Frequency      | Surfaces covered                                                                                                                     | First review due | Recurrence                  |
| --------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ---------------- | --------------------------- |
| **Quarterly**   | Every 90 days  | Repository collaborators (§2.1) · CI secret store inventory (§2.4) — the two highest-privilege, fastest-changing surfaces            | **2026-08-28**   | 2026-11-28, 2027-02-28, ... |
| **Semi-annual** | Every 180 days | Installed GitHub Apps (§2.2) · Authorized OAuth Apps (§2.3) — privileged but slower-changing third-party integration surfaces        | **2026-11-28**   | 2027-05-28, 2027-11-28, ... |
| **Annual**      | Every 365 days | Personal Access Tokens (§2.7) · Webhook receivers + deploy keys (§2.6) · Signing keys (§2.8) · Container registry push tokens (§2.5) | **2027-05-28**   | 2028-05-28, 2029-05-28, ... |

### Tier rationale

- **Quarterly for collaborators + CI secrets.** Repository
  collaborators are the highest-privilege grants in the project's
  surface — any collaborator with admin or write permission can
  alter `main`, modify the branch-protection ruleset, or push
  unauthorized release tags. CI secrets are the second-highest-
  privilege surface because a compromised CI secret enables
  arbitrary actions in the CI environment. Both surfaces change
  rapidly enough (collaborators on each contributor onboarding;
  secrets on each new third-party integration) that a 90-day cadence
  is the floor — anything less frequent risks missing a stale grant
  for a full quarter.
- **Semi-annual for GitHub Apps + OAuth Apps.** App installations
  and OAuth authorizations are slower-changing — once an App is
  installed and its permission scope is minimal, the day-to-day
  risk is low. Semi-annual cadence verifies (a) no new App was
  installed without going through a review, (b) installed Apps'
  scopes have not silently widened (which GitHub itself blocks
  without explicit user re-approval, but the verification confirms
  no re-approval was performed under coercion).
- **Annual for PATs + webhooks + signing keys.** PATs are
  longest-lived and most-private; the maintainer creates them
  rarely. Webhook receivers and deploy keys are similarly
  long-lived. Signing keys (GPG, cosign) are the longest-lived
  of all — rotation is rare because rotation is operationally
  expensive (re-signing past commits, re-publishing release
  signatures). Annual review is the right floor; tightening would
  produce review fatigue without proportional risk reduction.

### Why these specific dates

- **2026-08-28** as the first quarterly review is 90 days from this
  document's filing (2026-05-28). The 90-day interval is the
  natural starting cadence.
- **2026-11-28** as the first semi-annual review is 180 days from
  filing. It also coincides with the second quarterly review, so
  the maintainer can perform both during the same review session.
- **2027-05-28** as the first annual review is 365 days from filing.
  It coincides with:
  1. The third quarterly review.
  2. The second semi-annual review.
  3. **The first annual tabletop for slice 372 (IR plan) and
     slice 373 (BCP plan)** — co-scheduled deliberately so the
     maintainer performs three governance-cadence activities in
     a single week rather than three separate weeks.

### What happens if a review slips

A late review is a documented event, not a hidden one. If a
quarterly review slips past its target date:

1. The maintainer files a one-line entry in the next-available
   review artifact noting the slip and the rationale (illness,
   workload, no-changes-suspected, etc.).
2. The review is performed at the earliest feasible date after
   the slip.
3. If two consecutive cadence cycles slip without remediation,
   the maintainer escalates per [GOVERNANCE.md](../../GOVERNANCE.md)
   bus-factor & succession (the trigger is the "extended absence"
   pattern that GOVERNANCE.md operationalizes).

Slipping is not a normalized outcome. The intent is that
quarterly reviews complete within the 90-day window; the slip
provision exists to make the failure mode visible rather than
silent.

### Pre-cadence baseline reviews

The cadence above starts at 2026-08-28. This document is the
**first** governance commitment to access review — there is no
prior cadence to reconcile. The maintainer has performed informal
ad-hoc access checks (e.g., when adding a new CI workflow, the
maintainer informally confirmed the secret was scoped appropriately),
but these informal checks did not produce a documented evidence
artifact. **The 2026-08-28 quarterly review is the first formal
review under this plan.** This is named explicitly so the document
does not retroactively claim a review-cadence track record it does
not have.

---

## 4. Review procedure

The procedure below is the **copy-paste checklist** for each
scheduled review. The maintainer runs the steps for the tier
applicable, records the outcomes in the per-review artifact, and
files any revocations as CHANGELOG `### Security` bullets.

### 4.1 Quarterly review procedure

**Trigger:** scheduled date per §3, or out-of-band per §7.

**Step 1 — Enumerate repository collaborators.**

```bash
gh api repos/mgoodric/security-atlas/collaborators \
  --paginate \
  --jq '.[] | {login, role_name, permissions}'
```

For each row in the output:

- Confirm the collaborator is still expected.
- Confirm the role (`admin` / `maintain` / `write` / `triage` /
  `read`) is still minimal for the collaborator's needs.
- If revocation is warranted:
  - `gh api -X DELETE repos/mgoodric/security-atlas/collaborators/<login>`
  - Document the revocation in the review artifact with the
    rationale and the timestamp.
  - File a `### Security` CHANGELOG entry if the revocation is
    operator-visible (e.g., a previously-public collaborator is
    removed).

**Step 2 — Enumerate CI secrets.**

```bash
gh secret list --repo mgoodric/security-atlas
```

For each secret in the output:

- Confirm the secret is still referenced by at least one workflow
  in `.github/workflows/`. A secret with no references is stale
  and should be deleted: `gh secret delete <name> --repo mgoodric/security-atlas`.
- Confirm the secret's value is still current at its issuing
  surface (the third-party vendor, the GitHub PAT settings page,
  etc.).
- If a secret has been rotated at its issuing surface since the
  last review, confirm the GitHub-side value matches — a
  mismatched secret will cause CI failures and is itself a signal.

**Step 3 — Document outcomes.**

Create `docs/governance/access-reviews/YYYY-QN.md` (e.g.,
`2026-Q3.md` for the 2026-08-28 review) per the template in §6.

**Step 4 — File CHANGELOG entries.**

For any revocation or scope-reduction, add a `### Security` bullet
to the Unreleased section of `CHANGELOG.md`. Per the [IR plan §6
tone discipline](./incident-response.md#tone-discipline), the
bullet describes what happened factually without marketing voice.

### 4.2 Semi-annual review procedure

**Trigger:** scheduled date per §3, or out-of-band per §7.

**Step 1 — Enumerate installed GitHub Apps.**

```bash
gh api repos/mgoodric/security-atlas/installation \
  --jq '{app_slug: .app_slug, permissions: .permissions, created_at: .created_at, updated_at: .updated_at}'
```

For each App installation:

- Confirm the App is still needed for project operations.
- Walk the `permissions` block and confirm each permission's scope
  is still minimal — if any permission was added since the last
  review, confirm the addition was authorized.
- If revocation is warranted: uninstall via the GitHub UI at
  `https://github.com/mgoodric/security-atlas/settings/installations`.

**Step 2 — Enumerate authorized OAuth Apps.**

OAuth Apps authorized for the maintainer's account are visible at
`https://github.com/settings/applications`. The CLI surface for
listing is limited; the procedure is to visit the URL and review
the list manually.

For each authorized OAuth App:

- Confirm the App is still needed.
- Confirm the App's scope is still minimal.
- If revocation is warranted: revoke via the UI.

**Step 3 — Document outcomes** per §6 template; produce
`YYYY-H1.md` or `YYYY-H2.md` (e.g., `2026-H2.md` for the
2026-11-28 review).

**Step 4 — File CHANGELOG entries** for any revocations.

### 4.3 Annual review procedure

**Trigger:** scheduled date per §3, or out-of-band per §7.

**Step 1 — Enumerate Personal Access Tokens.**

GitHub does not provide a CLI surface for listing PATs.
The procedure is to visit `https://github.com/settings/tokens`
and review the list manually.

For each PAT:

- Confirm the PAT is still needed.
- Confirm the PAT's scope is still minimal.
- Confirm the PAT's expiration is set or planned (PATs without
  expiration are themselves a finding).
- If revocation is warranted: revoke via the UI.

The `BRANCH_PROTECTION_READ_TOKEN` documented by ADR-0005 is
expected on this list. Its presence is confirmed; its scope is
confirmed minimal (per ADR-0005, read-only on
`administration:read` only).

**Step 2 — Enumerate webhook receivers.**

```bash
gh api repos/mgoodric/security-atlas/hooks \
  --jq '.[] | {id, name, active, events, config_url: .config.url}'
```

For each webhook:

- Confirm the webhook is still needed.
- Confirm the destination URL (`config.url`) is still under
  legitimate ownership.
- If revocation is warranted:
  `gh api -X DELETE repos/mgoodric/security-atlas/hooks/<id>`.

**Step 3 — Enumerate deploy keys.**

```bash
gh api repos/mgoodric/security-atlas/keys \
  --jq '.[] | {id, title, read_only, created_at}'
```

For each deploy key:

- Confirm the key is still in use by its named deployment.
- If the key has not been used since the last review (verified
  by cross-reference to the deployment's own access log if one
  exists), consider revocation.
- If revocation is warranted:
  `gh api -X DELETE repos/mgoodric/security-atlas/keys/<id>`.

**Step 4 — Review signing keys.**

- **GPG signing key.** Confirm the key is still under maintainer
  control. Confirm the key has not expired. Confirm the key's
  fingerprint matches what GitHub displays at
  `https://github.com/mgoodric.gpg`.
- **cosign signing key** (when slice 368 lands). Per slice 368's
  design, confirm the cosign key is still under maintainer
  control and signatures verify against the published public key.

**Step 5 — Document outcomes** per §6 template; produce
`YYYY-annual.md` (e.g., `2027-annual.md` for the 2027-05-28
review).

**Step 6 — File CHANGELOG entries** for any revocations.

### Universal procedural notes

- **`gh api` output is held in the per-review artifact, not in
  this cadence document.** The cadence document is procedural;
  the artifact is forensic.
- **The maintainer signs the per-review artifact's commit with
  DCO sign-off.** No anonymous reviews.
- **The artifact is public by default**, same posture as the IR
  plan §8 (`docs/incidents/`). If specific output cannot be
  published (e.g., a webhook URL whose path itself is a secret),
  the artifact carries `[redacted — see private archive]` per
  the IR plan precedent.

---

## 5. Solo-maintainer considerations

The project currently operates with **a single maintainer**. This is
the load-bearing fact this section responds to. It mirrors the
solo-maintainer-honesty pattern established by the [IR plan §3
role devolution](./incident-response.md#solo-maintainer-role-devolution)
and the [BCP plan §3 role devolution](./business-continuity.md#solo-maintainer-role-devolution).

### The reviewer is the reviewed

In a multi-maintainer project, an access review is performed by
**someone other than the holder of the access being reviewed**. That
separation-of-duties is a control in itself: the reviewer's
independence from the reviewed grant reduces the risk of
rubber-stamping.

In this project, the reviewer **is** the holder of every grant
being reviewed. The maintainer reviews their own access. This is
not separation-of-duties; it is self-review.

The mitigations for the resulting risk are:

1. **The per-review artifact is public.** The artifact's
   public-by-default posture means a third-party observer can
   notice if a review fails to flag a stale grant or a
   privileged grant that should have been reduced. The
   transparency is the substitute for separation-of-duties.
2. **The cadence is documented.** A reviewer who silently skipped
   a review would leave the next-review-date conspicuously
   unmet; the slip provision in §3 makes the failure visible.
3. **The IR plan's post-incident review process** (per IR plan
   §9) provides a retrospective scrutiny surface — if an access
   review missed a grant that later led to an incident, the
   post-incident review surfaces the miss.

None of these mitigations are equivalent to true
separation-of-duties. They are the honest substitute available to
a sole-maintainer OSS project. This is named explicitly so a
third-party reviewer understands the control's actual shape rather
than its aspirational shape.

### Bus-factor and extended absence

The IR plan §3 and BCP plan §3 name the same bus-factor: 1. This
document inherits that constraint and adds one access-review-specific
implication:

- **An access review that does not occur is not a silent failure.**
  If the maintainer is unavailable for a full quarter, the
  quarterly review's slip is itself a continuity-event signal.
  The `docs/governance/access-reviews/` directory's last-entry
  date is a forensic record of how long ago the most recent review
  occurred.
- **The extended-absence trigger from GOVERNANCE.md** (the
  sealed-envelope mechanism the maintainer commits to
  documenting) covers the access-review surface implicitly: when
  the trusted contact opens the GitHub-org-transfer recovery
  path, the new maintainer's first action is a comprehensive
  access review against the surfaces inventoried in §2 of this
  document. **The first review under new maintainership is an
  out-of-band trigger per §7.**

### Re-evaluation when bus-factor improves

The [GOVERNANCE.md advisory-council formation
trigger](../../GOVERNANCE.md) (≥ 3 outside contributors with ≥ 6
months sustained involvement) is the named point at which this
section is re-evaluated. When that trigger fires:

1. The reviewer role is delegated to a co-maintainer or an
   advisory-council member — restoring separation-of-duties.
2. This section is updated to name the rotation.
3. The mitigations above are graduated to true controls rather
   than honest substitutes.

Until the trigger fires, single-person review is the honest
answer.

### What devolution does not include

- **A separate auditor.** No external auditor is engaged for
  access reviews. Each per-review artifact is self-attested by
  the maintainer.
- **An automated policy engine.** The reviews are human-driven.
  Automation is named in §8 as a hardening item; it is not
  committed today.
- **Re-attestation of the same grant by a separate person.** Each
  grant is attested once per review, by the maintainer.

---

## 6. Audit trail

Every access review is documented in one artifact:

**`docs/governance/access-reviews/YYYY-<period>.md`** — a per-review
Markdown file with the structure below. The artifact is public
unless redaction is required (per the IR plan §8 confidentiality
posture).

### File naming convention

- Quarterly: `2026-Q3.md`, `2026-Q4.md`, `2027-Q1.md`, ...
- Semi-annual: `2026-H2.md`, `2027-H1.md`, `2027-H2.md`, ...
- Annual: `2027-annual.md`, `2028-annual.md`, ...
- Out-of-band triggered: `YYYY-MM-DD-<trigger-slug>.md`
  (e.g., `2026-07-15-collaborator-departure.md`)

### Per-review artifact template

```markdown
+++
review_id = "YYYY-<period>"
review_type = "quarterly" | "semi-annual" | "annual" | "trigger-based"
scheduled_date = "YYYY-MM-DD"
performed_date = "YYYY-MM-DD"
reviewer = "<maintainer handle>"
trigger = "<scheduled>" | "<trigger description>"
+++

# Access review YYYY-<period>

**One-line summary.**

## Scope

<List of access surfaces reviewed in this artifact, by §2 reference.>

## Findings

### Collaborators (§2.1)

| Login | Role | Decision | Rationale |
| ----- | ---- | -------- | --------- |

### CI secrets (§2.4)

| Secret name | Workflow refs | Decision | Rationale |
| ----------- | ------------- | -------- | --------- |

### (other §2 sections as applicable to the review type)

## Revocations

- <List each revocation with timestamp and CHANGELOG cross-reference.>

## Scope reductions

- <List each scope-reduction with timestamp and CHANGELOG cross-reference.>

## Findings carried forward

- <Items surfaced but deferred to the next review or to a slice.>

## Slips and exceptions

- <If the review was late or skipped a section, document here.>

## Cross-references

- Previous review: `docs/governance/access-reviews/<prev>.md`
- Next review scheduled: YYYY-MM-DD
- CHANGELOG entries filed: <SHA links>
- Triggered slices: #NNN (if any)
```

The frontmatter is in TOML between `+++` markers (matching the
[IR plan §10](./incident-response.md#10-incident-log-template)
precedent). The machine-readable fields support future automation
(e.g., a script that surfaces overdue reviews; named in §8 as a
hardening candidate).

### CHANGELOG discipline

Every **revocation** or **scope-reduction** files a `### Security`
bullet in `CHANGELOG.md`'s Unreleased section. The bullet:

- Names the surface (collaborator, App, OAuth, secret, webhook,
  deploy key, PAT, signing key).
- Names the action (revoked, scope-reduced, replaced).
- Names the review artifact that drove the decision.
- Does **not** name the specific grant if naming it would aid a
  re-targeting attacker.

**Reviews that produce no revocations and no scope-reductions
do not file a CHANGELOG entry.** The per-review artifact is the
audit trail; an empty-result CHANGELOG bullet would be noise.

### Confidentiality

Per-review artifacts are **public** as part of the repository by
default. This mirrors the IR plan §8 transparency posture. If a
review surfaces material that must be redacted (e.g., a webhook URL
whose path is a secret, a personally-identifying detail of a
collaborator's affiliation), the redaction is recorded in the
artifact with a `[redacted — see private archive]` placeholder.
The unredacted material is held by the maintainer privately.

### Relationship to the IR plan

An access review that surfaces a grant that **should not exist**
(e.g., a collaborator the maintainer did not add; an App
authorization the maintainer did not grant; a PAT whose creation
the maintainer does not recall) is a finding that **promotes to
an incident** per the IR plan §2 reclassification provision. The
review artifact cross-references the incident log; the incident
log cross-references the review artifact. The two surfaces compose;
they are not duplicates.

---

## 7. Trigger-based reviews

In addition to the scheduled cadence in §3, an access review is
triggered out-of-band when any of the following occurs:

### 7.1 Collaborator or contributor departure

When a collaborator's relationship with the project ends (a paid
contractor's engagement ends; a volunteer contributor's
involvement lapses; the maintainer adds a contributor and later
needs to roll back), an out-of-band review of §2.1 collaborators
is triggered. The review artifact is filed at
`docs/governance/access-reviews/YYYY-MM-DD-collaborator-departure.md`
within 7 days of the departure event.

### 7.2 GitHub App rotation or replacement

When a GitHub App is rotated (an update is released that changes
the permission scope), is replaced (the project migrates to a
different App for the same function), or is uninstalled, an
out-of-band review of §2.2 is triggered. The review artifact is
filed within 7 days of the App change.

### 7.3 CVE published against an installed App or dependency

When a CVE is published against:

- An installed GitHub App with severity High or Critical.
- A CI workflow dependency (a third-party action, a runner image).
- A signing-key surface (a GPG vulnerability in the cryptographic
  library the maintainer's key was generated with; a cosign
  vulnerability).

…an out-of-band review of the affected surface is triggered. The
review composes with the IR plan's vulnerability-response playbook
(IR plan §7.3 dependency vulnerability) — the IR plan governs the
patch + advisory + CVE-assignment work; this document governs the
access-side decision (does the project still trust the App after
the CVE, with the new patch applied? does the project re-issue
the PAT after a credential-handling library CVE?).

### 7.4 Suspected credential compromise

When the maintainer has any reason to believe a credential has
been compromised — GitHub email about anomalous token use, a
GitGuardian alert on a leaked secret, a third-party report of
the credential in an unexpected location — an out-of-band review
of the affected credential is triggered immediately. The review
composes with the IR plan's auth-compromise playbook (IR plan
§7.2). The review artifact is filed within 24 hours of the
suspicion, regardless of whether the suspicion is later
confirmed or disproven.

### 7.5 Disclosure-triggered out-of-band review

When a coordinated-disclosure report (per [SECURITY.md](../../SECURITY.md))
identifies an access-control gap as the root cause or
contributing factor, an out-of-band review of the affected
surface is triggered as part of the IR plan's eradication phase.

### 7.6 Onboarding a new collaborator

When the maintainer adds a new collaborator to the repository,
an out-of-band review is **not** triggered — the addition itself
is the access decision. The next scheduled quarterly review
includes the new collaborator in its scope.

### 7.7 Major branch-protection ruleset change

When the branch-protection ruleset documented at
`.github/branch-protection.json` is materially modified (e.g.,
required-reviewer count changes, required-status-check list
changes, the rule's enforcement scope changes), the change PR
itself is the access decision and the next scheduled annual
review confirms the change is still warranted.

### Out-of-band review evidence

Out-of-band reviews use the same artifact template as scheduled
reviews (§6), with `review_type = "trigger-based"` and a `trigger`
field naming the specific trigger from §7.1-7.5. The artifact
files within 7 days of the trigger event (or 24 hours for §7.4
suspected compromise), and the next scheduled review confirms the
out-of-band review's findings remain valid.

---

## 8. Tooling and automation

### What is automated today

The cadence procedures in §4 are **manual**. The maintainer runs
the `gh api` queries, reviews the output, makes the decisions, and
files the artifact. There is no automated reminder that the next
review is due; no automated diff against the previous review's
output; no automated escalation when a review slips.

The only automation **adjacent** to access review today is:

- **Dependabot** auto-files PRs for dependency updates. This
  indirectly informs §7.3 trigger-based reviews when a Dependabot
  PR carries a security advisory.
- **GitGuardian** scans for committed secrets and alerts the
  maintainer. This indirectly informs §7.4 trigger-based reviews.
- **CodeQL and govulncheck** run on every push to `main`. These
  surface security findings that may trigger §7.3 reviews.
- **Branch protection** itself is the enforcement surface for
  collaborator permission tiers — a collaborator with `write`
  cannot bypass required reviews, regardless of whether the access
  review has happened or not.

None of these tools perform access review. They are detection and
prevention controls that **complement** the access-review surface.

### Automation candidates (named as hardening items, not committed)

The items below would materially improve the project's
access-review posture. They are **named here for visibility** so
the maintainer's annual review surfaces them for prioritization.
Each is named with the gap it closes. None are committed in this
slice.

| Item                                        | Gap it closes                                                                                                                                | Status                                                                                                                           |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| **Scheduled-review reminder action**        | Maintainer must remember the cadence; nothing auto-flags an overdue review                                                                   | Named; not committed. Could ship as a GitHub Action that opens an issue on the maintainer's repo at the due date                 |
| **Diff-against-previous-review automation** | Each review re-runs the `gh api` queries from scratch; nothing surfaces what changed since last time                                         | Named; not committed. Could ship as a script that consumes the previous artifact's frontmatter and computes the delta            |
| **Inactive-collaborator surfacing**         | A collaborator who has not interacted with the repository for N months should surface in the quarterly review automatically                  | Named; not committed. Requires querying contributor activity via `gh api`                                                        |
| **Stale-secret detection**                  | A CI secret that is no longer referenced by any workflow should be flagged at the quarterly review                                           | Named; not committed. Could ship as a script that cross-checks `gh secret list` against `grep -r 'secrets\.' .github/workflows/` |
| **PAT-expiration tracking**                 | A PAT without an expiration date is itself a finding; expirations approaching within the next 90 days should surface in the annual review    | Named; not committed. Requires periodic manual snapshot since the `https://github.com/settings/tokens` page is not CLI-queryable |
| **Repository-to-organization migration**    | A user-owned repo cannot use GitHub's organization-level audit log; migration to an org would give the project a richer access-audit surface | Named; not committed. Material project-governance decision; depends on GOVERNANCE.md re-evaluation trigger                       |
| **GitHub-native audit log subscription**    | If the repository migrates to a GitHub Enterprise org, GitHub's audit-log streaming API would let the project subscribe to access events     | Named; not committed. Depends on the migration above                                                                             |

### Why automation is not the first move

This document's primary commitment is **the cadence exists, the
procedure is documented, the evidence is produced**. Automation
amplifies a working manual process; it does not substitute for
one. Shipping automation in the same slice as the cadence would
risk the automation becoming the policy (the action runs,
therefore the review happens) rather than supporting the policy
(the action reminds the maintainer to perform the review).

The first three quarterly reviews (2026-08-28, 2026-11-28,
2027-02-28) will establish the manual baseline. At the
2027-05-28 annual review, the maintainer evaluates which of the
automation candidates above would close the gaps surfaced by the
manual-baseline experience, and files slices accordingly.

---

## 9. Maintenance

### Review cadence (of this document)

This document is reviewed **annually** by the maintainer,
co-scheduled with the [IR plan](./incident-response.md) and
[BCP plan](./business-continuity.md) tabletop. The next review of
this document is due **2027-05-28** — same date as the first annual
access review per §3.

The annual review of this document surfaces:

- Cadence tiers that proved unworkable in practice during the past
  year's quarterly / semi-annual / annual reviews.
- Procedural steps in §4 that did not match the actual `gh api`
  surface (the GitHub API evolves; commands may need updates).
- Access surfaces in §2 that have appeared since the last review
  (a new GitHub App installed; a new third-party integration; a
  new signing-key surface).
- Trigger conditions in §7 that proved over- or under-specified.
- Automation candidates in §8 that became prioritized work since
  the last review.
- Cross-references to other governance documents that have
  drifted (e.g., if the IR plan tone-discipline reference changes;
  if ADR-0005's PAT scope evolves).

The review's output is a PR that updates this file plus the annual
access review artifact at `docs/governance/access-reviews/YYYY-annual.md`.

### Ownership

The project maintainer owns this document. Changes follow the
standard slice / PR / DCO process documented in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md). Changes that materially
loosen the cadence (extend a quarterly to semi-annual; remove a
surface from §2) require an ADR.

### Relationship to ISO 27001 5.36

ISO 27001 5.36 ("Monitoring, review and change management of
information security") expects governance policies to be reviewed
on a fixed cadence with documented results. The annual review
cadence above is the project's commitment to that clause for the
access-review surface, mirroring the matching commitments in the
IR plan §12 and the BCP plan §11. Per the slice 329 audit report
§9, this commitment is recorded as a capability, not as a
certification claim.

### Relationship to canvas invariant #6

Canvas invariant #6 — **PostgreSQL Row-Level Security as the
tenant-isolation primitive at the database layer** — is the
constitutional commitment for **runtime** access control inside
the platform. This document is the analogous commitment for
**organizational** access control at the GitHub-repository boundary.
The two surfaces compose: invariant #6 enforces tenant separation
inside a running deployment; this document enforces a periodic
human review of who can modify the deployment substrate itself.
Both are non-negotiable, on different timescales.

### When to deviate from this plan

This plan describes the default cadence. The maintainer may
deviate when conditions demand it — for example, an access surface
expansion that warrants a one-time intermediate review between
scheduled cadences, or a security event that justifies a
comprehensive cross-tier review outside the scheduled rotation.
Deviations are documented in the per-review artifact with a
one-line rationale, per the IR plan §11 pattern.

---

## Document history

| Date       | Change                  | Slice |
| ---------- | ----------------------- | ----- |
| 2026-05-28 | Initial document filed. | 374   |
