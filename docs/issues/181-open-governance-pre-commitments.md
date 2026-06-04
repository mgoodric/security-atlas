# 181 — Open-governance pre-commitments (GOVERNANCE.md + funding signals + bus-factor plan)

**Cluster:** Docs / Governance
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Canvas open question #5 resolved 2026-05-20: Option A (pure community OSS, time-bounded). Five sub-decisions locked covering maintainership, funding posture, re-evaluation trigger, contributor agreements, and transparency. The resolution explicitly notes: **"making the COMMITMENT VISIBLE creates the trust that lets people contribute"** — i.e., GOVERNANCE.md is the bigger move than picking the model itself. This slice ships that visibility.

Today the project has Apache 2.0 (`LICENSE`), CONTRIBUTING.md with DCO discipline, README + canvas describing what we're building — but **no explicit governance statement**. Prospective adopters and contributors have to infer the model from absence: "is there a hosted SaaS coming? Is this a freemium funnel? Who has commit rights? What happens if Matt gets hit by a bus?" That ambiguity is a real trust gap.

**WHAT this slice ships.**

1. **`GOVERNANCE.md` at repo root** — the canonical governance statement. Sections: Model · Maintainership · Decision-making · Contributor agreements · Funding posture · Re-evaluation trigger · Bus-factor & succession.
2. **`.github/FUNDING.yml`** — GitHub Sponsors / OpenCollective entry, set up but understated (visible on GitHub UI; minimal README mention).
3. **README "Project status" section** near the top, stating: pure community OSS · no hosted SaaS · no enterprise edition · re-evaluation date · link to GOVERNANCE.md.
4. **CONTRIBUTING.md cross-link** to GOVERNANCE.md in the "Pull request workflow" section.
5. **Quarterly governance-checkin convention** — a documented format (`docs/audit-log/governance-checkin-<YYYY>-Q<N>.md`) so the maintainer captures the quarterly review of the re-evaluation trigger metrics + records the first one at slice merge time as a baseline.

**SCOPE DISCIPLINE — what's deliberately out.**

- **In-product telemetry** — explicitly NOT added. Self-host counting uses GitHub release-download stats as the proxy. OSS users hate telemetry; the trade-off isn't worth it for a count we can approximate.
- **Co-maintainer recruitment work** — recruiting a co-maintainer is a person-finding effort, not a slice. GOVERNANCE.md documents the commitment; the actual recruitment happens out-of-band.
- **Foundation transfer prep** — Option D path is preserved in GOVERNANCE.md's re-evaluation section but no work is done here. Premature.
- **Hosted SaaS / enterprise edition design** — explicitly NOT done now. Those evaluations happen at the trigger.
- **CLA introduction** — DCO-only stays; no CLA. Adding a CLA later would be a friction-introducing PR, not handled here.

## Threat model

Pure documentation slice; minimal threat surface.

**S — Spoofing.** None.

**T — Tampering.** GOVERNANCE.md is part of the repo trust root; if tampered with via a malicious PR, it could mislead prospective adopters. Mitigation: branch protection on `main` (already in place) requires PR review for any change; the doc is small enough that a tampering attempt would be obvious in a diff.

**R — Repudiation.** None new. GitHub keeps an audit trail of doc changes.

**I — Information disclosure.** The doc names the current maintainer (Matt Goodrich) — already public via git commit history + README. No new disclosure surface.

**D — Denial of service.** None.

**E — Elevation of privilege.** GOVERNANCE.md states who has commit rights. If the doc claims `[name]` has rights they don't have, that's a credibility surface, not a security one. Mitigation: the doc's commit-rights section names ONLY accounts that have actual GitHub repo permissions; cross-check at PR review.

**Verdict.** **CLEAN** — documentation slice; no new attack surface.

## Acceptance criteria

### GOVERNANCE.md (the load-bearing deliverable)

- **AC-1.** NEW `/GOVERNANCE.md` at repo root with the following H2 sections, in order: `## Model` · `## Maintainership` · `## Decision-making` · `## Contributor agreements` · `## Funding posture` · `## Re-evaluation trigger` · `## Bus-factor & succession`.
- **AC-2.** `## Model` section states explicitly: "security-atlas is a pure-community open-source project under the Apache 2.0 license. There is **no hosted SaaS** offered by the project owners. There is **no enterprise edition** with proprietary features. This posture is **time-bounded** — see Re-evaluation trigger below."
- **AC-3.** `## Maintainership` section names the current maintainer (Matt Goodrich, `mgoodric`). States the path to adding co-maintainers: ≥3 active outside contributors with ≥6 months of involvement triggers the formation of a small advisory council; until then, maintainer-led (BDFL).
- **AC-4.** `## Decision-making` section: BDFL for v1 product direction. Architecture decisions follow the canvas + slice + ADR convention already in place (`Plans/canvas/`, `docs/issues/`, `docs/adr/`). Major architectural pivots require an ADR. Routine product work flows through the slice pipeline.
- **AC-5.** `## Contributor agreements` section: DCO-only via `git commit -s` (links to CONTRIBUTING.md "Developer Certificate of Origin" section). Explicitly states "We do NOT require a CLA. DCO is sufficient for Apache 2.0 hygiene."
- **AC-6.** `## Funding posture` section: GitHub Sponsors + OpenCollective set up but not marketed. Corporate sponsorships welcomed via opening an issue with the `funding-discussion` label (label introduced in this slice). Project owner's separate consulting / conference / speaking income is explicitly outside the project's scope — the project receives funding only through documented sponsorship channels.
- **AC-7.** `## Re-evaluation trigger` section: explicit date (`2028-05-20`) AND metric (`100 deployed self-hosts proxied by GitHub release-download stats`). At trigger, the maintainer evaluates Options B (hosted SaaS) / C (enterprise edition) / D (foundation transfer) against then-current product + community signal. The evaluation is recorded as an ADR + canvas resolution update.
- **AC-8.** `## Bus-factor & succession` section: explicit listing of GitHub accounts with `Admin` or `Maintain` repo role (currently `mgoodric` only). States: if the maintainer becomes unable to maintain the project (incapacitation, prolonged absence), the GitHub org transfer + Apache 2.0 license + downstream forks ensure the code remains available — but active development pauses until a successor maintainer is identified. Commits to recruiting ≥1 co-maintainer in Year 1 (target: 2027-05-20).

### Funding signal infrastructure

- **AC-9.** NEW `.github/FUNDING.yml` with a GitHub Sponsors entry (`github: mgoodric`). The GitHub UI surfaces the sponsor button automatically; we don't link to it prominently from README.
- **AC-10.** README.md gains a NEW `## Project status` section after the title (before existing sections) with: pure community OSS · Apache 2.0 · no hosted SaaS · no enterprise edition · re-evaluation date · link to `GOVERNANCE.md`. ≤ 8 lines.

### Cross-links + ergonomics

- **AC-11.** CONTRIBUTING.md "Pull request workflow" section gains a one-line cross-link to GOVERNANCE.md.
- **AC-12.** New GitHub issue label `funding-discussion` created (via the slice; can be done at PR-merge time by maintainer or via a GitHub Actions workflow). NOT load-bearing; documented in GOVERNANCE.md AC-6.

### Quarterly governance-checkin

- **AC-13.** GOVERNANCE.md `## Re-evaluation trigger` section includes a paragraph explaining the quarterly checkin convention: maintainer records the trigger-metric state every quarter at `docs/audit-log/governance-checkin-<YYYY>-Q<N>.md`. Template fields: (a) GitHub release-download count delta this quarter; (b) GitHub stars delta; (c) new contributor count; (d) maintainer assessment "trigger fired Y/N"; (e) anything else worth flagging.
- **AC-14.** NEW file `docs/audit-log/governance-checkin-2026-Q2.md` as the **first quarterly checkin** (baseline snapshot at slice-merge time).

### Documentation

- **AC-15.** CHANGELOG entry under `[Unreleased] / Added`: "GOVERNANCE.md authored — open-governance pre-commitments per OQ #5 resolution (#181)."

## Constitutional invariants honored

- **OQ #3 resolution (Apache 2.0)** — GOVERNANCE.md affirms the license; does NOT propose relicensing.
- **OQ #5 resolution (Option A pure community OSS)** — this slice ships the visibility commitment that the OQ resolution explicitly identified as load-bearing.
- **DCO discipline (slice 005 onward)** — GOVERNANCE.md AC-5 reinforces the existing convention.

## Canvas references

- `Plans/canvas/11-open-questions.md` #5 (resolved 2026-05-20) — sibling resolution to this slice
- `Plans/canvas/11-open-questions.md` #3 (Apache 2.0 resolved 2026-05-13) — license foundation

## Dependencies

- **OQ #3 resolved** — Apache 2.0 locked. Prerequisite for stating "no relicensing" credibly.
- **OQ #5 resolved** — Option A locked. This slice is the implementation of OQ #5's pre-commitments.
- No code-slice dependencies.

## Anti-criteria (P0 — block merge)

- **P0-181-1.** Does NOT promise or commit to a hosted SaaS / enterprise edition / dual license model. GOVERNANCE.md explicitly states "no hosted SaaS at this time" + "no enterprise edition" without ruling out future evolution at the re-evaluation trigger.
- **P0-181-2.** Does NOT bake-in telemetry. Self-host counting uses GitHub release-download stats as the proxy. No in-product telemetry pings introduced in this slice OR mentioned as a future plan in GOVERNANCE.md.
- **P0-181-3.** Does NOT introduce a CLA. DCO-only stays.
- **P0-181-4.** Does NOT prominently feature funding links in README (just FUNDING.yml + the GitHub UI's automatic sponsor button). README's "Project status" section may mention sponsorships are welcome but the section's primary purpose is governance clarity, not fundraising.
- **P0-181-5.** Does NOT remove the GitHub Sponsors / OpenCollective set-up after merge. Once visible on GitHub UI, the signal exists.
- **P0-181-6.** Re-evaluation trigger MUST be REAL — both a date (2028-05-20) AND a metric (100 deployed self-hosts via release-download proxy), not "we'll see how it goes". Vague triggers fail the discipline check.
- **P0-181-7.** Bus-factor / succession section is NOT cosmetic. It names actual GitHub accounts with admin/maintain rights AND states the contingency (Apache 2.0 + forks preserve the code; active development pauses until co-maintainer identified). Empty platitudes fail.
- **P0-181-8.** Quarterly governance-checkin format is real and concrete (template fields enumerated in GOVERNANCE.md). The first checkin (`governance-checkin-2026-Q2.md`) is COMMITTED in this slice as the baseline.
- **P0-181-9.** Does NOT replace LICENSE, CONTRIBUTING.md, or README. Adds GOVERNANCE.md alongside them. Cross-links only; no content duplication.

## Skill mix (3-5)

1. **OSS governance literacy** — reference shapes from Kubernetes, OpenTelemetry, Backstage, GitLab GOVERNANCE.md documents
2. **GitHub repo features** — FUNDING.yml schema, label management, GitHub Sponsors flow
3. **CONTRIBUTING.md / README authorship** — existing voice + cross-link discipline

## Notes for the implementing agent

### GOVERNANCE.md reference style

Look at three exemplars before drafting (do NOT copy text — copy STRUCTURE):

- Kubernetes: <https://github.com/kubernetes/community/blob/master/governance.md> (heavy; foundation-shaped)
- OpenTelemetry: <https://github.com/open-telemetry/community/blob/main/community-membership.md> (community-tier-driven)
- Backstage: <https://github.com/backstage/backstage/blob/master/GOVERNANCE.md> (CNCF-graduated; explicit about model evolution)

Our shape is closer to Backstage's — but solo-maintainer-shaped, not yet foundation-graduated. Target length: ~150-250 lines. Concrete > comprehensive.

### Re-evaluation trigger date math

The resolution locked **2028-05-20** — exactly 2 years from the OQ #5 resolution date. Engineer at pickup: confirm this date in GOVERNANCE.md verbatim. Do NOT recompute (the date is the load-bearing anchor; recomputation drift creates trust friction).

### Bus-factor section honesty

GOVERNANCE.md AC-8 demands honesty about the bus-factor problem. Do NOT soften the language. Sample text: "security-atlas currently has a single primary maintainer. If the maintainer becomes unable to maintain the project (incapacitation, prolonged absence), the project's Apache 2.0 license and the immutability of git history ensure the code remains available. Downstream forks may continue development. Active development on this repository, however, pauses until a successor maintainer is identified. Reducing this risk is an explicit Year 1 priority; the maintainer commits to recruiting at least one co-maintainer by 2027-05-20."

This is the kind of statement that creates trust precisely because it doesn't sugarcoat.

### Spillover candidates the grill surfaced

If during this slice an out-of-scope finding emerges:

- **Co-maintainer recruitment workflow** — a separate slice (or operator workflow) to identify + onboard the first co-maintainer. Document the process; don't try to recruit-via-PR.
- **GitHub Sponsors tier design** — if sponsorships start flowing in, the maintainer may want named tiers ("Bronze / Silver / Gold supporter"). Don't design tiers now; let them emerge from real conversations.
- **Foundation prep** — premature.
- **CONTRIBUTING.md polish** — if drafting GOVERNANCE.md reveals gaps in CONTRIBUTING.md, file a separate slice; don't refactor CONTRIBUTING.md in this PR.

### Provenance

Filed 2026-05-20 as the pre-commitment foundation for the OQ #5 resolution (Option A pure community OSS). Maintainer accepted all five sub-decisions in the same session that filed slices 180 (OQ #7) + 179 (OQ #9 + #17). This slice ships the governance visibility commitment.
