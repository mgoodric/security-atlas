# Incident response plan

**Status:** Active governance document.
**Filed:** 2026-05-28 by slice 372.
**Closes:** Slice 329 compliance meta-audit finding **H-1** (no documented incident response plan).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual. Next review: 2027-05-28.

---

## Why this document exists

[`SECURITY.md`](../../SECURITY.md) covers the **inbound** side of security
work — how a researcher reports a vulnerability, the acknowledgement
timeline, the coordinated-disclosure policy. It does not cover what happens
**after** a vulnerability lands in the maintainer's lap, or what happens
when an operational incident (build pipeline compromised, dependency
publishes a malicious version, CI runner reports a finding the maintainer
must triage in flight) needs to be worked.

This document fills that gap. It is short by design — the maintainer must
be able to read it under stress.

This document **describes capabilities**, not certifications. security-atlas
is not SOC 2 certified, not ISO 27001 certified, not HIPAA-attested. The
plan below documents how the project responds to incidents today; it does
not claim third-party attestation of that response.

Cross-references:

- [`SECURITY.md`](../../SECURITY.md) — inbound vulnerability reporting (the intake surface this plan feeds from).
- [`GOVERNANCE.md`](../../GOVERNANCE.md) — bus-factor and succession (what happens if the maintainer is unavailable).
- [`CHANGELOG.md`](../../CHANGELOG.md) — the `### Security` section is the public-facing audit trail for security-relevant fixes.
- [`docs/adr/0003-oauth-authorization-server.md`](../adr/0003-oauth-authorization-server.md) — the auth substrate that several incident playbooks below touch.
- [`docs/audits/327-security-audit-security-auditor-report.md`](../audits/327-security-audit-security-auditor-report.md) — inventory of monitored-for failure modes.
- [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) — the chaos-experiment design backlog that doubles as the self-test substrate for this plan.

---

## 1. Purpose and scope

### What this plan covers

This plan is about incidents affecting **the security-atlas project itself**:

- The `mgoodric/security-atlas` GitHub repository, including releases, the
  container registry (`ghcr.io/mgoodric/security-atlas`), the docs site,
  and the CI/CD pipeline.
- Security vulnerabilities in the platform code that have been disclosed
  to the maintainer (per `SECURITY.md`'s intake process) and are now under
  active triage.
- Compromise of project-controlled third-party services (Codecov,
  GitGuardian, GitHub Actions runners, dependency registries the project
  depends on).
- Operational events with a security signal — for example, a Dependabot
  alert that crosses a triage threshold, a CodeQL finding on `main` that
  was not surfaced during PR review, a CI failure pattern that suggests a
  systemic issue.

### What this plan does not cover

- **Incidents inside operator-hosted deployments.** Operators self-hosting
  security-atlas are responsible for their own IR plan. Where this plan
  is useful as a starting template, that is an explicit secondary benefit;
  it is not a guarantee.
- **Customer-side compliance program incidents.** If an operator using
  security-atlas has an incident inside their security program (a missing
  control, a failed audit period), that flows through the platform's
  product surface, not this document.
- **Code of Conduct violations.** Those are governed by
  [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md).
- **General bug reports.** Public GitHub issues are the channel for those.

### What counts as an incident

A condition is an **incident** when it meets at least one of the following:

1. A reported or discovered vulnerability with a CVSS-equivalent rating of
   medium or higher in any security-atlas-published code or container.
2. A confirmed or strongly-suspected compromise of a project-controlled
   account, secret, key, or service.
3. A confirmed or strongly-suspected unauthorized modification to `main`
   or to a published release artifact.
4. A CI/CD failure pattern that materially blocks releases for more than
   24 hours and has a security or integrity dimension (a malicious
   dependency, a compromised runner image, a GitGuardian alert that
   cannot be triaged within the standard SLA).

Bug reports, dependency updates surfaced by Dependabot under the medium
threshold, and CI flakes that do not have a security dimension are
**operational issues**, not incidents. They are tracked through the
normal slice / PR pipeline.

---

## 2. Incident severity tiering

The four severity tiers below are the project's contract with itself —
they bind response speed, escalation, and communications. The tiers map
to SOC 2 CC7.3-CC7.4, ISO 27001 5.24-5.26, and NIST SP 800-61r3 incident
handling conventions.

| Tier   | Definition                                                                                                              | Examples                                                                                                                                                                                                                                                                                                    | Maintainer ack target                             | Containment target                   | Fix target                                                                  |
| ------ | ----------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------- | ------------------------------------ | --------------------------------------------------------------------------- |
| **P0** | Active exploitation in the wild OR confirmed compromise of project secrets / releases / `main` with continuing exposure | A published container image is confirmed backdoored. The maintainer's GitHub PAT has been confirmed used by a third party. A `main` commit contains a credential that is currently being scraped. A CVE is being actively exploited against deployed self-hosts.                                            | **24 hours** (best-effort outside business hours) | **72 hours**                         | **7 days** for a public patch + advisory; expedited release outside cadence |
| **P1** | Confirmed vulnerability or compromise; no evidence of active exploitation                                               | A reported auth-bypass with a working proof-of-concept. A confirmed credential leak detected by GitGuardian on a feature branch. A high-CVSS dependency CVE with a known upstream patch. ISO-27001-relevant control gap surfaced by a quarterly audit and rated High.                                       | **5 business days** (SECURITY.md SLA)             | **10 business days**                 | **30 days** (SECURITY.md SLA for high / critical)                           |
| **P2** | Suspected vulnerability or compromise; needs investigation to confirm or disprove                                       | A static-analysis finding that may or may not be exploitable. A surprising authentication log line that may indicate misuse. A Dependabot medium-severity alert. A CI scanner finding under the auto-merge threshold that needs human triage.                                                               | **10 business days**                              | N/A (investigation, not containment) | **Next regular release** (typically <60 days)                               |
| **P3** | Security-relevant operational issue with low immediate risk                                                             | An advisory about a GitHub Actions runner image vulnerability that the project does not consume. A deprecation notice for a cryptography library used in a low-risk path. A documentation correction with security implications. A CI scanner adds a new informational rule and surfaces existing findings. | **20 business days** (best-effort)                | N/A                                  | **Next regular release** or **no fix needed**                               |

### Tier calibration notes

- **P0 vs P1 hinges on exploitation evidence, not on theoretical impact.**
  A 9.8 CVSS in code path that no operator has reached and that no
  exploit code exists for is P1, not P0. A 5.5 CVSS being actively
  exploited is P0, not P1.
- **The 24-hour P0 ack is best-effort outside business hours.** The
  maintainer is a single person; this plan does not promise 24/7 on-call.
  Within business hours (US Pacific, M-F), P0 ack target tightens
  informally to within the day.
- **P1 targets align with SECURITY.md.** The 5 / 10 / 30 business-day
  cadence in SECURITY.md is reproduced here so the two documents do not
  drift. If they ever diverge, SECURITY.md is the source of truth for
  inbound disclosure timing.
- **P2 is the most common tier in practice.** Most incidents will start
  P2 and reclassify (up or down) within the first 24-48 hours of
  investigation.

### Reclassification

Severity is not fixed at incident open. The Incident Commander
(see Section 3) reclassifies whenever new information surfaces. Every
reclassification is recorded in the incident log (Section 10) with a
one-line rationale.

---

## 3. Roles and responsibilities

The standard NIST SP 800-61r3 incident roles are:

| Role                        | Responsibility                                                                                                                       |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Incident Commander (IC)** | Owns the incident end-to-end. Sets severity, drives containment, decides communications timing, authorizes the post-incident review. |
| **Tech Lead**               | Executes containment, eradication, and recovery. Writes the fix, drives reproduction, validates the patch.                           |
| **Comms Lead**              | Drafts and sends external communications (advisories, CHANGELOG entries, reporter responses, public statements).                     |
| **Scribe**                  | Maintains the incident log in real time. Captures decisions, timestamps, and links to artifacts.                                     |
| **On-call**                 | Receives and triages new reports. Promotes a report to incident status if criteria are met.                                          |

### Solo-maintainer role devolution

**The project currently operates with a single maintainer.** All five
roles above devolve to that maintainer. This is the bus-factor problem
that [`GOVERNANCE.md`](../../GOVERNANCE.md) names plainly under "Bus-factor
& succession", and this section operationalizes the response-side
implication.

In practice this means:

- **The maintainer is the IC + Tech Lead + Comms Lead + Scribe**, in
  parallel, during an active incident.
- **There is no second pair of eyes during the incident.** Recovery
  review (the post-incident review at Section 9) is the substitute —
  it surfaces decisions made under pressure for retrospective scrutiny.
- **The on-call rotation is named "single-person on-call".** New reports
  via `SECURITY.md`'s GitHub Private Vulnerability Reporting (PVR) channel
  or email arrive in the maintainer's inbox. Acknowledgement is
  best-effort during business hours; outside business hours,
  acknowledgement is best-effort for P0 only.
- **There is no formal escalation path.** If the maintainer is
  unavailable, see [`GOVERNANCE.md`](../../GOVERNANCE.md) "Bus-factor &
  succession" — the project's escalation answer is the documented
  GitHub-org-transfer recovery path, not a 24/7 backup engineer.

### When the role-stacking becomes untenable

The GOVERNANCE.md advisory-council trigger (≥ 3 active outside
contributors with ≥ 6 months sustained involvement) is the named point
at which this section is re-evaluated. When that trigger fires:

1. The maintainer recruits at least one co-IC.
2. This section is updated to name the rotation.
3. The post-incident-review template is updated to include explicit
   sign-off from a second reviewer.

Until then, single-person on-call is the honest answer.

### What devolution does **not** include

- **24/7 phone tree.** Not offered. P0 outside business hours is
  best-effort.
- **Pager-equivalent automation.** Not committed. The maintainer
  monitors GitHub notifications, email, and the SECURITY.md PVR queue;
  no separate alerting service is named.
- **Spokesperson for press inquiries.** Not offered. The maintainer
  responds in writing through the documented channels in Section 6;
  there is no phone or in-person media relations.

---

## 4. Detection

Incidents enter the response pipeline through one of the following
sources:

| Source                                                                                                  | What it surfaces                                                                                                                                                                                                                                                                         | Routing                                                                                                                                          |
| ------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| **GitHub Private Vulnerability Reporting** (`SECURITY.md`)                                              | Externally-disclosed vulnerabilities — the primary inbound channel                                                                                                                                                                                                                       | Maintainer email + GitHub advisory queue                                                                                                         |
| **Email to maintainer** (per `SECURITY.md` fallback)                                                    | Reporters who prefer email over the GitHub form                                                                                                                                                                                                                                          | Maintainer inbox; subject line tagged `[security-atlas]`                                                                                         |
| **CI scanner alerts** (CodeQL, govulncheck, Trivy, Dependabot, GitGuardian, StepSecurity Harden-Runner) | Static analysis, dependency vulnerabilities, secrets in commits, supply-chain advisories                                                                                                                                                                                                 | GitHub Security tab; maintainer notification on every push to `main` and weekly scheduled scans                                                  |
| **Build / release pipeline failures**                                                                   | Release-please failures, container build failures, ghcr.io push failures, image-signing (slice 368 cosign) failures                                                                                                                                                                      | GitHub Actions workflow notifications                                                                                                            |
| **Public GitHub issues**                                                                                | Sometimes a reporter opens a public issue without realizing it's a security report. SECURITY.md explicitly asks for private reporting; when a public issue appears that should be private, the maintainer redirects the reporter and **redacts the original issue** as fast as feasible. | The maintainer triages public issues as part of normal slice-pipeline work; security-tagged ones promote to incident status immediately          |
| **Quarterly audit cycle** (slice 327 / 329 cadence)                                                     | Findings from quarterly security and compliance audits                                                                                                                                                                                                                                   | Findings of severity High or Critical become incidents on the audit's filing day; Medium-and-below findings become slices in the normal pipeline |
| **Chaos experiments** (slice 335 backlog, v2+ execution)                                                | Hypotheses that falsify under controlled injection                                                                                                                                                                                                                                       | When the chaos backlog executes, a falsified hypothesis becomes an incident at minimum P2 severity                                               |
| **Maintainer self-discovery**                                                                           | The maintainer notices something while doing other work — a log line, a sudden traffic pattern, a config drift                                                                                                                                                                           | Promoted to incident at whatever severity the maintainer judges; no formal escalation                                                            |

### What is monitored (the "what to detect for" inventory)

The slice 327 security audit verified the technical control surface that
this plan responds to. For the purposes of incident detection, the
project monitors for:

- Authentication anomalies (OIDC token replay, JWT forgery, session
  hijack — instrumented via the unified audit log at
  `migrations/sql/20260517000000_unified_audit_log.sql`).
- Authorization failures spiking (signal of credential stuffing or
  policy misconfiguration).
- Dependency vulnerabilities (Dependabot weekly; CodeQL weekly).
- Secret leakage in commits (GitGuardian wired into branch protection).
- Container image vulnerabilities (Trivy per build).
- Go vulnerability database hits (govulncheck per CI run).
- Supply-chain integrity (StepSecurity Harden-Runner audit-mode hook;
  cosign image signing per slice 368 once landed).
- Five-hundred-class HTTP response patterns (slice 367 generic-error
  helper means a sudden surge of 500s is its own signal).
- Unusual `main`-branch commit patterns (force-pushes blocked by branch
  protection; unsigned commits blocked by DCO check; unreviewed
  commits blocked by required-reviewer rule).

The above list is the working answer to "what could go wrong that we
would notice." It is not exhaustive. As the project evolves, this
section is updated alongside the slice 327 cadence (annual).

---

## 5. Response workflow

The project follows the standard NIST SP 800-61r3 incident lifecycle:

```
   detect -> triage -> contain -> eradicate -> recover -> review
                  |__________________________|_________|
                                |
                         escalate / reclassify
                            as needed
```

Each stage is described below with the per-tier playbooks in Section 7
filling in the detail for common incident shapes.

### 5.1 Triage (all tiers)

When a new condition is reported or discovered, the maintainer:

1. **Acknowledges receipt** within the tier's ack target (Section 2).
   For GitHub PVR reports the acknowledgement is a comment on the
   advisory; for email it's a reply.
2. **Opens an incident log** at `docs/incidents/YYYY-MM-DD-<slug>.md`
   (see Section 10 for template). The first entry is timestamped within
   minutes of acknowledgement.
3. **Assigns a tier** per Section 2. If unsure between two tiers,
   choose the higher one — reclassification down is cheap.
4. **Decides whether the condition is an incident**. If the answer is
   "no", the condition routes back to normal slice pipeline and the
   incident log is closed with a one-line "not promoted" note.

### 5.2 Containment (P0 / P1)

The goal of containment is to **stop the bleeding**, not to fix the
root cause. Containment is acceptable to leave in place for the
duration of the eradication window.

Generic containment actions:

- For **credential compromise**: revoke the credential. For PATs:
  GitHub Settings → Personal access tokens → Revoke. For deploy keys:
  delete the key. For API keys issued by the platform: use the
  `security-atlas-cli credentials revoke <id>` workflow (slice 003).
- For **malicious code on `main`**: revert the commit. If the
  problem is a published release, **yank the release tag** and
  publish a corrected one; document the yank in CHANGELOG.md
  `### Security`.
- For **a backdoored container image**: delete the affected image
  tags from ghcr.io. Publish a new image with the original digest
  the maintainer signed.
- For **a leaking secret**: rotate the secret immediately. Treat the
  pre-rotation secret as compromised regardless of whether the leak
  is confirmed exploited.
- For **a CI runner compromise advisory**: pin the runner image to
  the last known-good version pending upstream patch.

Containment actions are **always documented** in the incident log as
they happen, with timestamp and the artifact reference.

### 5.3 Eradication

The goal of eradication is to **remove the root cause**, not just the
visible symptom.

Generic eradication actions:

- For **vulnerabilities**: ship a patch through the normal slice +
  PR process. The slice doc lives at `docs/issues/NNN-incident-<slug>.md`
  and cross-references the incident log. The PR carries the
  `security` label.
- For **compromised secrets**: confirm the new secret is in place
  AND the old one has been revoked across every system that
  referenced it. Update the asset inventory (slice 376 once landed)
  if the secret's location is not already recorded.
- For **supply-chain compromise**: bump the dependency, audit
  adjacent dependencies for the same compromise vector, run the
  full CI suite, and verify the affected code paths still behave as
  expected.
- For **process / configuration root causes**: update the
  configuration in-tree (e.g., branch protection, pre-commit
  config, CI workflow). The configuration change is itself a PR.

### 5.4 Recovery

The goal of recovery is to **restore steady state** and confirm the
incident is closed.

Recovery is complete when **all** of the following hold:

1. The eradication PR is merged to `main`.
2. A release is cut (or the eradication is hotfixed into the latest
   release tag, per `SECURITY.md`'s out-of-band-release provision).
3. The CHANGELOG `### Security` entry is published.
4. The reporter (for inbound disclosures) is notified that the fix has
   shipped.
5. The advisory (for CVE-worthy issues) is published with a CVE
   identifier requested through GitHub.
6. The incident log is updated with the recovery timestamp.

For operational incidents where there is no external reporter, items
4-5 above do not apply.

Roll-back considerations for the deploy substrate are documented at
[`deploy/observability/README.md`](../../deploy/observability/README.md);
slice 364 (OTel namespace strip) is an example of the kind of rollback
document the project produces for operator-facing changes. Operator-side
incidents may require pointing operators at upgrade or rollback paths via
the release notes.

### 5.5 Review (mandatory for P0 and P1; optional for P2 and P3)

See Section 9.

---

## 6. Communications

The communications playbook is intentionally narrow. The project has
no PR function, no marketing team, no spokesperson rotation. What it
has is a documented set of channels with clear triggers.

| Channel                             | When to use                                                                                                                                                                                              | Who writes              |
| ----------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| **Reporter response** (PVR / email) | Always, when a reporter exists. First reply within the ack target; subsequent replies on material status changes (containment in place, fix merged, release shipped).                                    | Comms Lead (maintainer) |
| **GitHub Security Advisory**        | Always, for P0 / P1 with confirmed vulnerability. Filed in `draft` status during eradication and published on coordinated-disclosure date.                                                               | Comms Lead              |
| **CVE assignment**                  | For any P0 / P1 with a CVE-worthy impact (per `SECURITY.md` "Disclosure policy"). CVE is requested through GitHub's CVE Numbering Authority workflow at advisory-publication time.                       | Comms Lead              |
| **`CHANGELOG.md` `### Security`**   | Every security incident that resulted in a code change. The CHANGELOG entry links the incident log, the advisory, the slice, and the PR.                                                                 | Comms Lead              |
| **GitHub release notes**            | For releases that contain a security patch — the `### Security` CHANGELOG entry is reproduced at the top of the release notes.                                                                           | release-please workflow |
| **`SECURITY-ACKNOWLEDGEMENTS.md`**  | When a reporter has authorized public credit (per `SECURITY.md` "Recognition"). The file is created on first use; until then the SECURITY.md forward-reference is a known low-priority gap (audit L-2).  | Comms Lead              |
| **Email to known operators**        | Reserved for P0 with active exploitation. The project does not currently maintain an operator mailing list; if one is established (future slice), it is used here.                                       | Comms Lead              |
| **Public statement on the repo**    | Reserved for incidents where the project's reputation or trust posture is materially affected — for example, a confirmed maintainer-account compromise. The statement goes in `SECURITY-INCIDENTS.md` at | Comms Lead              |
|                                     | the repo root; this file does not exist by default and is created only when needed.                                                                                                                      |                         |

### What the project does **not** communicate

- **Speculative impact.** Until containment is confirmed, the project
  does not publish severity ratings or affected-version ranges.
- **Reporter identity** without explicit permission.
- **Internal investigation steps** in the public advisory. The advisory
  documents impact, affected versions, and the fix; it does not
  publish the investigation timeline beyond what the reporter agreed to.

### Tone discipline

Communications follow the tone discipline from slice 337
([`docs/governance/board-narrative-tone-anti-patterns.md`](board-narrative-tone-anti-patterns.md)):
measured, factual, no marketing voice, no unprompted superlatives.
"We have addressed this vulnerability" rather than "we are proud to
announce that we have leveraged industry-leading techniques to resolve
this vulnerability."

---

## 7. Per-tier playbooks

The four playbooks below cover the most common incident shapes the
project expects to handle. They are not exhaustive; they are
calibration examples. The IC adapts them as the incident requires.

### 7.1 Data leak — secret committed to the repository

**Detection.** GitGuardian alert on push to `main` or feature branch.
Maintainer self-discovery of a key in an unexpected commit.

**Tier.** P1 by default. Reclassify to P0 if the key is confirmed
in active use by a third party.

**Containment.**

1. **Rotate the secret immediately** — assume it is compromised even
   if no abuse signal exists yet.
2. If the secret is in a not-yet-merged feature branch, **force-push
   over the commit** to remove the visible reference. (Force-push is
   normally blocked on `main` by branch protection; for feature
   branches it is permitted.)
3. For `main`, the secret cannot be removed from history without a
   filter-branch / BFG rewrite. **Do not rewrite `main`.** Instead,
   rely on rotation as the containment mechanism — the secret-on-record
   is no longer valid.
4. Record the leak in the incident log with the file path, the commit
   SHA, and the rotation timestamp.

**Eradication.**

1. Add the leaked credential's pattern to GitGuardian's allowlist if
   it was a false-positive (rare).
2. If it was a real leak, add a pre-commit hook or CI rule that
   would have caught the leak earlier (e.g., a project-specific
   regex).
3. File a slice via `/idea-to-slice` to ratchet the detection logic.

**Recovery.**

1. Confirm rotation across every system that referenced the old
   credential.
2. CHANGELOG `### Security` entry; do not name the specific service
   if doing so would aid an attacker re-targeting.
3. Update the asset inventory (slice 376 once landed) if the asset
   location was not previously documented.

### 7.2 Auth compromise — maintainer GitHub PAT in third-party hands

**Detection.** Email from GitHub indicating a token use from an
unexpected location. Maintainer notices a PR or comment they did not
author. Third-party security researcher reports observing the token
in a public location.

**Tier.** P0 unconditionally — this is the bus-factor scenario in
miniature.

**Containment.**

1. **Revoke every PAT in the maintainer's GitHub account** — not
   just the suspected one. The maintainer cannot rule out that
   other tokens are also compromised.
2. **Disable any GitHub App or OAuth grant** the maintainer did not
   recently authorize.
3. **Force-revoke active SSH session and OAuth sessions** from
   GitHub Settings.
4. **Audit `mgoodric/security-atlas` for unauthorized changes** —
   `git log` since the last verified-clean commit (the most recent
   release tag the maintainer cosigned, when slice 368 lands;
   meanwhile, the most recent commit the maintainer remembers
   authoring).
5. **Audit branch protection ruleset** — `gh api repos/mgoodric/security-atlas/rulesets`
   — for any rule changes.

**Eradication.**

1. Generate new PATs with minimum scope; document the new scopes
   in ADR-0005's table.
2. Confirm `BRANCH_PROTECTION_READ_TOKEN` and any other CI-side
   PATs are rotated.
3. Investigate the leak vector — phishing, malware, repo misconfig,
   or third-party service compromise (Codecov, GitGuardian, etc.) —
   and add a control to address the root cause.

**Recovery.**

1. Publish a public statement at `SECURITY-INCIDENTS.md` confirming
   the compromise was contained.
2. CHANGELOG `### Security` entry.
3. If any operator-affecting artifact (release tag, container image)
   was modified during the compromise window, **yank the artifact**
   and publish a corrected one with a clear advisory.

### 7.3 Dependency vulnerability — high-CVSS CVE in transitive dependency

**Detection.** Dependabot weekly scan; govulncheck per build;
Trivy per build; CodeQL weekly.

**Tier.** P1 if exploit code is available or the CVE is in a hot
code path. P2 if the CVE is in a cold path or no exploit is known.
P3 if security-atlas does not actually reach the vulnerable code.

**Containment.**

For P1 dependency vulns, containment is **not** the natural first
move — the project is not the upstream; the natural move is
eradication via dependency bump. However, if the CVE is being
actively exploited and no upstream fix is yet available:

1. Identify whether the vulnerable code path is reachable from a
   security-atlas entry point. If not, downgrade to P2.
2. If reachable, evaluate whether the affected feature can be
   temporarily disabled via configuration (feature flag, if applicable).
3. If neither is possible, document the residual risk in the
   incident log and proceed directly to eradication.

**Eradication.**

1. Bump the dependency to the patched version through a PR.
2. Audit adjacent dependencies for the same compromise vector.
3. Run the full CI suite — Go unit, Go integration, frontend
   vitest, Playwright e2e (per the four enforced surfaces in
   CLAUDE.md "Testing discipline").
4. For breaking-change bumps, file a slice and follow the normal
   review process.

**Recovery.**

1. Cut a patch release if the project is between releases.
2. CHANGELOG `### Security` entry naming the CVE.
3. GitHub Security Advisory if security-atlas itself is
   vulnerable to the CVE (not just because of dependency name);
   skip if the project was not actually exposed.

### 7.4 Deploy break — release pipeline compromised or producing bad artifacts

**Detection.** Release workflow failure with anomalous error. ghcr.io
push failure. Trivy reports an unexpected layer in a build artifact.
A user reports a corrupt container image.

**Tier.** P0 if a bad artifact has been published. P1 if the
compromise is detected pre-publish. P2 if it's a misconfiguration
without compromise signal.

**Containment.**

1. **Pause the release pipeline** — disable the workflow on the
   repository via Actions UI.
2. **Yank the affected artifact** if one was published. For ghcr.io
   container images: delete the tagged image and republish from a
   known-good commit. For GitHub releases: keep the tag, mark the
   release as "Pre-release" with a security note, and publish a
   corrected release.
3. **Audit recent workflow runs** for anomalies in inputs, outputs,
   or runner environment.

**Eradication.**

1. Identify the compromise vector — was it the runner image, a
   third-party action, a credential, or the workflow definition?
2. Address the root cause in a PR. Pin third-party actions to a
   specific SHA if not already pinned. Bump runner image. Rotate
   the credential. Update the workflow.
3. Re-enable the workflow.

**Recovery.**

1. Publish a corrected release.
2. CHANGELOG `### Security` entry naming the vector.
3. Operator-facing release notes describe the bad artifact, the
   correction, and the verification path (e.g., re-pull the image
   with the new tag).
4. If image signing has landed (slice 368), the corrected image
   is signed; operators can verify the signature before redeploying.

---

## 8. Documentation and audit trail

Every incident is documented in two artifacts:

1. **The incident log** at `docs/incidents/YYYY-MM-DD-<slug>.md` — a
   per-incident Markdown file with the structure in Section 10. This
   is the primary record.
2. **`CHANGELOG.md` `### Security`** — for incidents that result in a
   code change, the changelog entry is the public-facing audit trail.
   It links the incident log, the advisory, the slice doc, and the PR.

### File naming

- Incident logs: `docs/incidents/YYYY-MM-DD-<slug>.md` where `YYYY-MM-DD`
  is the detection date and `<slug>` is a short kebab-case description
  (e.g., `docs/incidents/2026-06-15-pat-leak-feature-branch.md`).
- A small number of incidents per year is the realistic baseline; if
  the cadence exceeds 12 per year, that is itself a signal worth
  investigating.

### Confidentiality

Incident logs are **public** as part of the repository by default. This
is the project's working assumption — transparency over discretion. If
an incident contains material that must be redacted (reporter PII,
third-party PII, or details that would aid an attacker re-targeting the
same vector), the redaction is recorded in the log with a placeholder
(`[redacted — see private archive]`). The unredacted version is held
by the maintainer privately and is **not** part of the public
repository.

### Cross-references in the incident log

Every incident log links:

- The originating detection source (GitHub PVR advisory ID, email
  thread, CI workflow run, audit report).
- The eradication slice doc and PR.
- The CHANGELOG entry.
- The GitHub Security Advisory and CVE, if applicable.
- The post-incident review (Section 9), if one was conducted.

---

## 9. Post-incident review

The post-incident review is the project's mechanism for **learning
from incidents**. It is **mandatory for P0 and P1**, optional for P2
and P3.

The review is blameless. The maintainer is not in a position to
"blame" anyone else (single-person on-call); the review's only
function is to surface what could detect the incident earlier next
time and how the project's control surface should evolve.

### Template

The post-incident review is appended to the incident log under the
heading `## Post-incident review`. The template is:

```markdown
## Post-incident review

**Review date:** YYYY-MM-DD
**Reviewers:** <maintainer> (+ co-IC if one exists)

### Timeline

| Time (UTC) | Event |
| ---------- | ----- |
| ...        | ...   |

### What happened

<One-paragraph factual summary. No interpretation.>

### Root cause

<The technical / process / human factor that allowed the incident.>

### What went well

<Things the response surface did right. Cite specifically.>

### What went poorly

<Things that were slower or less complete than they should have
been. Specific failures, not general handwaving.>

### What could detect this earlier

<The single most valuable question. What signal — if monitored —
would have surfaced this before it became an incident?>

### Action items

- [ ] <Slice / PR / config change — filed as a specific follow-up
      with an owner and a target date. Action items live as
      forward links to slice docs filed via /idea-to-slice.>

### Slices spawned

- #NNN — <short description>

### What we are explicitly not doing

<Containment-style commitments we decided NOT to make and why. This
section guards against over-correction.>
```

### Action item discipline

- **Every action item becomes a slice or a PR.** No action item is
  left as a "todo" in the incident log.
- **Action items use `/idea-to-slice`** to file the slice. The slice
  doc cross-references the incident log.
- **The maintainer reviews open action items at every quarterly
  governance checkin** (per `GOVERNANCE.md`'s quarterly cadence).
  Stalled action items are explicitly acknowledged with a status
  update — no silent dropping.

### Public vs. private review

The post-incident review is **public** by default, same as the
incident log. The exception is if the review contains attack-vector
detail that would aid re-targeting; in that case the public review
documents the high-level lessons and the detailed root cause is held
in the private archive (consistent with Section 8's confidentiality
provision).

---

## 10. Incident log template

Every incident gets a file at `docs/incidents/YYYY-MM-DD-<slug>.md`
using this template:

```markdown
+++
incident_id = "YYYY-MM-DD-<slug>"
severity = "P0" | "P1" | "P2" | "P3"
status = "open" | "contained" | "eradicated" | "resolved" | "not-promoted"
discovered_at = "YYYY-MM-DDTHH:MM:SSZ"
acknowledged_at = "YYYY-MM-DDTHH:MM:SSZ"
contained_at = "YYYY-MM-DDTHH:MM:SSZ"
resolved_at = "YYYY-MM-DDTHH:MM:SSZ"
reporter = "<github handle, email, or 'self-discovery'>"
incident_commander = "<maintainer handle>"
+++

# Incident YYYY-MM-DD-<slug>

**One-line summary.**

## Cross-references

- Detection source: <PVR advisory / email thread / CI run / audit report>
- Eradication slice: docs/issues/NNN-<slug>.md
- Eradication PR: gh#NNN
- CHANGELOG entry: <commit SHA / release tag>
- GitHub Security Advisory: GHSA-xxxx-xxxx-xxxx
- CVE: CVE-YYYY-NNNNN
- Post-incident review: see below

## Timeline

| Time (UTC) | Event |
| ---------- | ----- |
| ...        | ...   |

## Containment actions

- ...

## Eradication actions

- ...

## Recovery confirmation

- ...

## Post-incident review

<See Section 9 of docs/governance/incident-response.md for template.>
```

The frontmatter is in TOML between `+++` markers (matching the
project's CODE_OF_CONDUCT.md frontmatter convention). The
machine-readable fields support future automation (e.g., an
auto-generated dashboard of open incidents).

---

## 11. Testing the plan

The plan is tested by **tabletop exercises** and by **chaos experiments**.

### Tabletop exercises

**Cadence: annual.** Once per year, the maintainer runs a tabletop
exercise — walking through a hypothetical incident from detection
to recovery using this document and confirming each step is
actionable.

The first tabletop is due **2027-05-28** (one year from this
document's filing date).

Tabletop output is recorded at
`docs/audit-log/tabletop-YYYY-MM-DD.md` and:

- Surfaces any step in this plan that proved unactionable.
- Files slices to address gaps.
- Updates this document inline where the playbook is wrong.

### Chaos experiments

The slice 335 chaos-experiment design backlog
([`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md))
defines eight chaos experiments. As those experiments execute (v2+
slices 354-358), they serve as **operational** tests of the response
machinery — when a falsified hypothesis triggers an incident, the
incident is worked through this plan and the response surface itself
is exercised.

The maintainer treats chaos-experiment-driven incidents the same as
externally-triggered ones: same incident log, same severity rubric,
same post-incident review. The only difference is the detection
source.

### Audit cycle as a test

The quarterly security and compliance audit cadence (slice 327 +
329 + similar future audits) is itself a passive test of this plan:
audit findings rated High or Critical become incidents on the
audit's filing day. If the audit cycle stops surfacing findings,
that is itself a signal worth investigating.

---

## 12. Maintenance

### Review cadence

This document is reviewed **annually** by the maintainer. The next
review is due **2027-05-28**.

The annual review surfaces:

- Tier definitions that proved unworkable in practice.
- Playbooks that did not fit real incidents.
- Communication-channel additions or retirements (e.g., if an
  operator mailing list is established).
- Role devolution updates (e.g., if the advisory council from
  GOVERNANCE.md forms).
- Cross-references that have drifted (e.g., if SECURITY.md SLAs change).
- New detection sources added since the last review.
- Tabletop and chaos-experiment outcomes since the last review.

The review's output is a PR that updates this file plus an annual
review note at `docs/audit-log/incident-response-review-YYYY.md`.

### Ownership

The project maintainer owns this document. Changes follow the
standard slice / PR / DCO process documented in
[`CONTRIBUTING.md`](../../CONTRIBUTING.md).

### Relationship to ISO 27001 5.36

ISO 27001 5.36 ("Monitoring, review and change management of
information security") expects governance policies to be reviewed
on a fixed cadence with documented results. The annual review
cadence above is the project's commitment to that clause. Per
Section 9 of the slice 329 audit report, this commitment is
recorded as a capability, not as a certification claim.

### When to deviate from this plan

This plan describes the default response. The IC may deviate
when an incident's shape demands it — for example, an incident
involving a third party whose disclosure requirements override the
project's normal communications timing. Deviations are documented
in the incident log with a one-line rationale.

---

## Document history

| Date       | Change                  | Slice |
| ---------- | ----------------------- | ----- |
| 2026-05-28 | Initial document filed. | 372   |
