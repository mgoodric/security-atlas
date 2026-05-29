# 373 — Business continuity / disaster recovery plan (governance document)

**Cluster:** Governance
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Slice 329's compliance meta-audit
(`docs/audits/329-compliance-meta-audit-report.md` finding **H-2**, severity
**High**) surfaced that the project has no documented Business Continuity /
Disaster Recovery plan for its own properties. SOC 2 Availability TSC
(A1.2 + A1.3) is unauditable without RTO/RPO statements; ISO 27001 5.29-30
require continuity arrangements; HIPAA 164.308(a)(7) requires a contingency
plan.

`docs/SELF_HOSTING.md` documents **operator-side** backup (pg_dump nightly
to S3) — that is customer guidance, not project DR. GOVERNANCE.md mentions
a bus-factor / succession plan but doesn't operationalize RTO/RPO for the
docs site, container registry (`ghcr.io/mgoodric/security-atlas`), release
pipeline, or GitHub-side repo loss.

**What ships.** A new governance document at
`docs/governance/business-continuity.md` covering:

1. **Asset-criticality map** — for each project property (GitHub repo,
   ghcr.io image registry, docs site at GitHub Pages, release pipeline,
   Codecov + GitGuardian integrations), state the criticality (Critical /
   Important / Nice-to-have) and the impact of a 24h outage on operators.
2. **RTO / RPO targets per asset class** — explicit and honest. The repo
   itself: RTO 24h (mirror restoration from local maintainer clone), RPO 0
   (DCO + signed commits provide content recoverability). Container
   registry: RTO 7 days (rebuild from source on a fresh ghcr.io path), RPO
   N/A (images are reproducible from tagged commits). Docs site: RTO 24h
   (re-deploy from `main`), RPO 0.
3. **GitHub-loss recovery procedure** — what happens if the GitHub repo is
   suspended / deleted / lost. References the maintainer's local mirror as
   the canonical recovery source; suggests a periodic off-GitHub mirror
   (e.g., to a self-hosted Gitea or to a Codeberg mirror) as a hardening
   option.
4. **Maintainer-unavailable handoff procedure** — pointer to
   GOVERNANCE.md's bus-factor / succession plan with the operational steps:
   who in the advisory council (when one exists) inherits release-signing
   keys, branch-protection-PAT rotation rights, etc. Honest acknowledgment
   that at the sole-maintainer stage the bus-factor is 1 and the project
   would experience a temporary outage if the maintainer were unavailable.
5. **Tabletop exercise cadence** — annual tabletop reviewing the plan
   against then-current threats; output is an entry in
   `docs/audit-log/tabletop-YYYY-MM.md`.
6. **Cross-references** to GOVERNANCE.md (succession), SECURITY.md (the
   incident-response side of recovery), `docs/SELF_HOSTING.md` (operator-
   side backup guidance).

**No code modified.** Pure documentation slice.

## Threat model

Document-only slice. STRIDE pass:

- **S/T/R:** No new auth surface; document edits only.
- **I:** The BCP plan tells an attacker "here are the assets we care about
  and how we recover." **Mitigation:** stay at asset-class level (repo,
  registry, docs site) without listing specific automation paths, named
  cloud accounts, or backup credentials. The recovery procedure says WHAT
  is rebuilt, not WHERE specific secrets live.
- **D:** Document existence doesn't enable DoS.
- **E:** N/A.

## Acceptance criteria

- [ ] **AC-1.** `docs/governance/business-continuity.md` exists with the
      six sections above.
- [ ] **AC-2.** Asset-criticality map enumerates the project's properties
      with criticality + 24h-outage-impact text.
- [ ] **AC-3.** RTO/RPO targets per asset class are honest (sole-maintainer
      reality, NOT aspirational SaaS-vendor targets).
- [ ] **AC-4.** GitHub-loss recovery procedure documents the local-mirror-
      restore path as the canonical recovery source.
- [ ] **AC-5.** Maintainer-unavailable handoff procedure references
      GOVERNANCE.md succession.
- [ ] **AC-6.** Tabletop exercise cadence documented (annual); template
      lands as `docs/audit-log/tabletop-template.md`.
- [ ] **AC-7.** CHANGELOG.md Unreleased `### Documentation` bullet records
      the new policy.
- [ ] **AC-8.** No code modified — diff = governance doc files only.
- [ ] **AC-9.** `pre-commit run --files <touched paths>` passes.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** Direct closure for
  the Availability TSC half — a reviewer asking "what's your RTO?" gets a
  documented answer instead of "we don't have one."
- **No over-engineering (canvas anti-patterns).** RTO/RPO are calibrated
  for a sole-maintainer OSS project, not for a regulated SaaS vendor.
  Aspirational 99.99% claims explicitly avoided.

## Canvas references

- `Plans/canvas/01-vision.md §6` — survive third-party review
- `GOVERNANCE.md` — bus-factor / succession

## Dependencies

- **#329** (compliance meta-audit) — `merged` at this slice's spawn time.
- **#372** (IR plan) — sibling; can land in parallel.

## Anti-criteria (P0 — block merge)

- **P0-373-1.** Does NOT make availability claims the maintainer cannot
  unilaterally deliver (e.g., no "99.99% docs-site uptime SLA" — false; the
  honest target is best-effort with documented recovery procedures).
- **P0-373-2.** Does NOT include exploit-roadmap detail (e.g., named cloud
  accounts, specific backup paths, secret-handling specifics).
- **P0-373-3.** Does NOT modify code.
- **P0-373-4.** Does NOT auto-merge.
- **P0-373-5.** Does NOT modify GOVERNANCE.md's succession plan content —
  this slice cross-references; succession-plan revisions are a separate
  governance slice.

## Notes for the implementing agent

**Tone discipline.** Sole-maintainer-honest. Aspirational claims are worse
than honest gaps — the gap is documented and the auditor can scope
appropriately; the inflated claim destroys credibility on the spot.

**Length target.** ~150-300 lines of Markdown.

**Asset list seeding.** Use slice 376's asset inventory as a starting list
if 376 lands first; otherwise this slice enumerates the assets de novo and
slice 376 references it.

**Decision log.** File
`docs/audit-log/373-business-continuity-plan-decisions.md` recording the
RTO/RPO calibration choices, the asset-criticality grading method, and the
tabletop-cadence justification.
