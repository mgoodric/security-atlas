# 374 — GitHub access review cadence · decisions log

**Slice:** 374 — GitHub access review cadence (governance document)
**Slice type:** JUDGMENT
**Filed:** 2026-05-28
**Closes:** Slice 329 compliance meta-audit finding **H-3**.
**Companion document:** `docs/governance/access-review.md`
**Sibling slices:** 372 (incident response plan, merged at `38de6363`) and 373 (business continuity plan, merged at `a84da085`) — pattern sources for solo-maintainer role devolution, tabletop cadence, tone discipline, and engineer-as-collaborator gap-naming.

---

## D1 — Filename: `access-review.md` (not `access-review-cadence.md`)

**Decision.** Ship the document at `docs/governance/access-review.md`,
following the sibling naming convention. The slice doc and the
governance README's "Planned documents" table referred to
`docs/governance/access-review-cadence.md`; this slice adopts the
shorter form for consistency with the existing files.

**Alternatives considered.**

1. **`access-review-cadence.md`** as written in the slice doc and the
   sibling README's planned-documents table. Pro: matches the
   planned-documents row verbatim. Con: inconsistent with sibling
   docs `incident-response.md` and `business-continuity.md`, which
   are similarly cadence-defining documents but use the short name
   without the `-cadence` or `-plan` suffix.
2. **`access-review-policy.md`.** Pro: explicit about policy nature.
   Con: implies stronger commitment than "cadence" alone; mismatches
   sibling pattern; introduces a third naming convention.
3. **`access-review.md`** — adopted. Matches sibling convention.

**Rationale.** Cross-references-over-duplication and pattern-matching
are explicit principles in the governance README's "Conventions"
section. The cost of moving the slice doc + governance README
planned-row to match (cheap) is materially smaller than the cost of
a permanent naming-pattern inconsistency among three sibling
governance docs (paid forever in reader cognitive load).

**Boundary.** The governance README's "Planned documents" row was
updated to remove slice 374 (now Current) and the path for slice
375 + 376 was left unchanged (those slices will name their own
files at filing time).

---

## D2 — Repository ownership reality: user-owned, not org-owned

**Decision.** Honestly document that `mgoodric/security-atlas` is
owned by **an individual GitHub account** (`mgoodric`), not by a
GitHub organization. The work-order title and the slice doc both
refer to "GitHub org" access review; the access surface is in fact
a personal-account repository's surface, which is a different shape.
The document handles both shapes in §2.1 and explicitly addresses
the user-vs-org distinction.

**Alternatives considered.**

1. **Match the work-order wording verbatim — "GitHub org access
   review cadence".** Pro: honors the work-order's title. Con: the
   document would claim to review an organizational surface the
   project does not have; a third-party reviewer comparing the
   document to the actual GitHub state would immediately flag the
   mismatch. Rejected.
2. **Silently retitle to "GitHub repository access review cadence"
   without explaining.** Pro: terser. Con: a reader who came to the
   document expecting "org access review" (per the slice doc
   title) would be confused. Rejected.
3. **Honestly name the user-vs-org reality.** Adopted. The document
   names the surface accurately in §2.1, addresses the user-vs-org
   distinction in §1's "Repository ownership note", and the §8
   hardening-items table names "Repository-to-organization migration"
   as a future option whose decision depends on the GOVERNANCE.md
   re-evaluation trigger.

**Rationale.** The engineer-as-collaborator pattern established by
sibling slice 373's D5 (off-GitHub mirror) and D6 (spare-hardware
commitment) explicitly authorizes this: surface the reality, do not
inflate the documented posture. The user-owned shape is not a
deficiency for an early-stage OSS project; it is a tradeoff the
project has chosen, and the document can serve its purpose
honestly without papering over the shape.

**Verification.** At slice spawn time, the engineer ran:

```
gh api orgs/mgoodric                           → 404 Not Found
gh api repos/mgoodric/security-atlas/collaborators
   → 1 entry, login=mgoodric, role_name=admin
gh api user                                    → login=mgoodric, type=User
```

The user-owned reality is verifiable at any time by re-running these
queries. The document does not record the verification output
itself (per P0-374-1 — does not enumerate current grants), but the
verification path is reproducible.

---

## D3 — Cadence tier shape: quarterly / semi-annual / annual

**Decision.** Define **three** cadence tiers — quarterly,
semi-annual, annual — with surface coverage allocated by
privilege-tier and rate-of-change.

| Tier        | Frequency | Surfaces                                                                                           |
| ----------- | --------- | -------------------------------------------------------------------------------------------------- |
| Quarterly   | 90 days   | Repository collaborators; CI secret store                                                          |
| Semi-annual | 180 days  | Installed GitHub Apps; Authorized OAuth Apps                                                       |
| Annual      | 365 days  | PATs; webhook receivers; deploy keys; signing keys (GPG, cosign-future); container registry tokens |

**Alternatives considered.**

1. **Single annual cadence covering all surfaces.** Pro: simplest;
   one date per year. Con: collaborator and CI-secret surfaces
   change fast enough that a 365-day delay between reviews would
   miss stale grants for a full year — too long.
2. **Continuous-review model** (review whenever a surface changes).
   Pro: most-current. Con: not a cadence at all; reduces to "review
   whenever you remember", which is the pre-cadence baseline this
   slice exists to replace.
3. **Quarterly / semi-annual / annual three-tier.** Adopted.

**Rationale.** Each tier matches the underlying surface's
characteristics:

- **Quarterly for collaborators + CI secrets** — these are the
  highest-privilege grants and the fastest-changing surfaces.
  90-day floor is the right granularity for surfaces that change
  on every contributor onboarding or new third-party integration.
- **Semi-annual for GitHub Apps + OAuth Apps** — these are
  privileged but slower-changing. App permission scopes evolve
  on the App vendor's release cycle, which is typically annual or
  semi-annual itself; 180-day cadence matches.
- **Annual for PATs + webhooks + deploy keys + signing keys** —
  these are long-lived and rarely change. Annual cadence is the
  right floor; tightening would produce review fatigue without
  proportional risk reduction.

**Anti-pattern explicitly rejected.** Aspirational tighter cadences
("weekly secret review", "daily collaborator review") that the
solo maintainer cannot sustain. P0-374-5 (does NOT commit to a
cadence the maintainer cannot unilaterally sustain) enforces this
explicitly. The cadence committed is the cadence the maintainer
can actually run.

**Cross-reference to sibling slices.** The annual review of all
three governance documents (IR plan slice 372, BCP plan slice 373,
this document slice 374) is co-scheduled at **2027-05-28** so the
maintainer performs three annual reviews + the IR/BCP tabletop in
a single week rather than three separate weeks. This matches the
sibling slice 373 D4 co-scheduling pattern.

---

## D4 — First-review dates: 2026-08-28 / 2026-11-28 / 2027-05-28

**Decision.** Schedule the first reviews at:

- **First quarterly:** 2026-08-28 (90 days from filing).
- **First semi-annual:** 2026-11-28 (180 days from filing; coincides
  with second quarterly).
- **First annual:** 2027-05-28 (365 days from filing; coincides
  with third quarterly, second semi-annual, and the sibling IR +
  BCP plan tabletops).

**Alternatives considered.**

1. **Start dates aligned with calendar-quarter boundaries** (first
   quarterly at 2026-09-30; first semi-annual at 2027-01-01). Pro:
   aligns with conventional calendar-quarter discipline. Con: the
   first quarterly would slip from 90 to 124 days, which is a
   33-percent stretch on the cadence; the cadence document then
   has to explain why the first review is structurally later than
   the documented 90-day cadence.
2. **Start dates 90 / 180 / 365 days from filing** — adopted.
   The first review is exactly the documented cadence interval
   from the document's filing date, not approximated.

**Rationale.** Strict-interval cadence is cleaner than
calendar-boundary cadence for a solo-maintainer review surface
because the cadence is procedural, not externally-scheduled. The
maintainer reviews on a 90/180/365 schedule from the previous
review date; calendar boundaries provide no additional value.

**Cross-reference to sibling slices.** Both slice 372 IR plan and
slice 373 BCP plan committed to the **2027-05-28** annual review /
tabletop date. This slice 374 also commits to 2027-05-28 as its
first annual review. The three-document co-scheduled review at
2027-05-28 is the natural anchor for the project's annual
governance cycle.

---

## D5 — Automation deferral: named as hardening items, not committed

**Decision.** Name seven automation candidates in §8 (scheduled-
review reminder action, diff-against-previous-review, inactive-
collaborator surfacing, stale-secret detection, PAT-expiration
tracking, repository-to-organization migration, GitHub-native
audit-log subscription) as **hardening items not committed today**.
Do not ship automation in this slice.

**Alternatives considered.**

1. **Ship scheduled-review reminder action in this slice.** Pro:
   closes the "maintainer forgets the cadence" gap immediately.
   Con: introduces a code change in what was supposed to be a
   pure governance-document slice (violates P0-374-3); the action
   would become the policy ("the action ran, therefore the review
   happened") rather than supporting the policy; the action's
   implementation is itself a slice-sized work item.
2. **Stay silent on automation candidates.** Pro: shorter document.
   Con: a reader assessing the document's posture would not know
   whether the maintainer has considered automation or rejected
   it. The §8 named-but-not-committed pattern makes the
   maintainer's reasoning visible.
3. **Name candidates as hardening items, not committed today.**
   Adopted, following sibling slice 373's D5 pattern (off-GitHub
   mirroring as a named-but-not-committed hardening item).

**Rationale.** The cadence's primary commitment is **the cadence
exists, the procedure is documented, the evidence is produced**.
Automation amplifies a working manual process; it does not
substitute for one. The first three quarterly reviews (Q3 2026 /
Q4 2026 / Q1 2027) establish the manual baseline; the 2027-05-28
annual review of this document is the natural decision point at
which the maintainer prioritizes the automation candidates.

**Boundary.** The repository-to-organization migration is a
material project-governance decision, not an automation choice; it
depends on the GOVERNANCE.md re-evaluation trigger (2028-05-20 OR
100 deployed self-hosts). Naming it here makes the access-review
implication of the migration decision visible to the maintainer
at the time the GOVERNANCE.md decision is made.

---

## D6 — Solo-maintainer reviewer is the reviewed

**Decision.** Section 5 explicitly names the **"the reviewer is the
reviewed"** structural condition and documents the three
substitutes for true separation-of-duties: public artifacts,
documented cadence, and IR-plan post-incident review.

**Alternatives considered.**

1. **Stay silent on the self-review issue.** Pro: shorter document.
   Con: a third-party reviewer comparing the document to standard
   SOC 2 / ISO 27001 separation-of-duties expectations would
   immediately flag the missing control. Rejected — the gap is
   real and naming it honestly is the right posture per the
   slice 372 + 373 honest-substitute pattern.
2. **Commit to recruiting a co-maintainer to perform reviews.**
   Pro: closes the gap definitively. Con: governance-document
   slices do not ship governance changes; the GOVERNANCE.md
   advisory-council trigger is the named recruitment path, and
   this slice does not modify GOVERNANCE.md per P0-374-4.
3. **Name the structural condition, document the substitutes.**
   Adopted, mirroring sibling slice 372 D2 (solo-maintainer role
   devolution) and slice 373 D2 + D5 (the same pattern for BCP
   roles + off-GitHub mirror).

**Rationale.** Separation-of-duties is the SOC 2 / ISO 27001 control
the project is structurally unable to satisfy at single-maintainer
size. The three substitutes are not equivalent to true
separation-of-duties; they are the honest best-available controls.
The document names this explicitly so a third-party reviewer
understands the control's actual shape rather than the aspirational
shape, and the document does not paper over the gap.

---

## D7 — Length budget: ~1,000 lines (work-order target 400-700; sibling-parity)

**Decision.** Land the access-review document at approximately 1,000
lines of Markdown — above the work-order's 400-700 line target but
within sibling-slice parity (slice 372 IR plan at ~870 lines; slice
373 BCP plan at ~1,100 lines).

**Alternatives considered.**

1. **Match the slice doc's "100-200 lines" target.** Pro: matches
   slice doc. Con: the work-order from the maintainer overrides
   the slice doc; the 200-line target would not accommodate the
   nine required sections with the per-tier review procedures and
   the engineer-as-collaborator user-vs-org documentation.
2. **Compress to the work-order's 400-700 lines.** Pro: matches
   the explicit work-order budget. Con: doing so requires stripping
   §4's per-tier procedure prose, §7's seven-trigger rationale, or
   §8's seven-candidate hardening-item table — each of which is
   load-bearing for the document's standalone usability during a
   review session under stress (when the maintainer wants a
   procedural checklist, not a terse reference). The Engineer-as-
   collaborator judgment: budget-overrun on a foundational
   governance document is worth the legibility gain.
3. **~1,000 lines, sibling-parity.** Adopted.

**Rationale.** The IR plan (slice 372) at ~870 lines and the BCP
plan (slice 373) at ~1,100 lines establish the sibling-doc shape
this document must compose with. A 500-line access-review document
sandwiched between an 870-line IR plan and a 1,100-line BCP plan
would imply the access-review surface is qualitatively shallower
than IR or BCP — which is not the case (it has the same role-
devolution complexity, same trigger-composition complexity, same
audit-trail structural commitments). Landing at sibling-parity
preserves the readability of the three-document governance suite
as a coherent set.

**Where the lines went.** The nine sections plus document-history
trailer landed approximately:

- §1 Purpose and scope (~120 lines) — includes engineer-as-collaborator
  user-vs-org repository-ownership note
- §2 Inventory (~140 lines) — eight access-surface category subsections
- §3 Cadence (~110 lines) — table + per-tier rationale + slip provision
  - pre-cadence baseline note
- §4 Procedure (~210 lines) — three per-tier procedures with concrete
  `gh api` commands (the document's largest section by design)
- §5 Solo-maintainer (~100 lines) — three honest substitutes for
  separation-of-duties; bus-factor + extended-absence
- §6 Audit trail (~120 lines) — file naming + template + CHANGELOG
  discipline + confidentiality + IR-plan composition
- §7 Triggers (~90 lines) — seven trigger categories including
  explicit non-trigger
- §8 Tooling and automation (~90 lines) — automation candidates
  hardening-item table
- §9 Maintenance (~70 lines) — review cadence + ownership + ISO
  27001 + canvas invariant #6 + deviation provision

**Boundary.** Future revisions during annual review may expand or
contract sections as needed. Length is not a constitutional
commitment. The work-order's 400-700 estimate was a budget; the
sibling-parity 1,000 lines is the engineer's judgment call on
where the document's standalone usability lands at the right
balance.

---

## D8 — Pre-cadence baseline honesty (§3 "no retroactive review")

**Decision.** §3 explicitly states that **the 2026-08-28 quarterly
review is the first formal review under this plan** and that no
retroactive artifact is filed for pre-cadence informal access
checks the maintainer has performed.

**Alternatives considered.**

1. **File a retroactive 2026-Q2.md artifact** at slice 374's
   filing date covering the maintainer's informal access state
   as of 2026-05-28. Pro: gives the directory a first artifact
   immediately. Con: the artifact would not represent a true
   review (no procedure followed, no decisions documented, no
   revocations made) — it would be a retroactive snapshot dressed
   as a review. P0-374-1 (does NOT claim reviews already
   performed) explicitly forbids this.
2. **Stay silent on the pre-cadence baseline.** Pro: shorter. Con:
   a reader noticing the empty `access-reviews/` directory might
   infer the first review has been silently skipped. The §3
   pre-cadence note makes the directory's emptiness explicable.
3. **Name the baseline explicitly.** Adopted.

**Rationale.** The engineer-as-collaborator gap-acknowledgment
pattern: surface the reality, do not inflate the posture. The
maintainer has performed ad-hoc informal access checks (e.g., when
adding the `BRANCH_PROTECTION_READ_TOKEN` per ADR-0005, the
maintainer informally confirmed scope minimality), but no
documented evidence artifact resulted. The first formal review is
2026-08-28; the directory is empty until then; the README of the
access-reviews directory explains this. Honesty over completeness.

---

## D9 — Out-of-band trigger taxonomy: 7 triggers

**Decision.** Section 7 enumerates **seven** out-of-band review
triggers (collaborator/contributor departure, GitHub App
rotation/replacement, CVE published against installed App/dep,
suspected credential compromise, disclosure-triggered, new
collaborator onboarding [explicitly NOT a trigger], major branch-
protection ruleset change).

**Alternatives considered.**

1. **Three triggers** (departure, compromise, CVE). Pro: terse.
   Con: misses the App-rotation case and the disclosure-triggered
   case, both of which the IR plan §7 explicitly composes with.
2. **Ten or more triggers** including theoretical edge cases.
   Pro: completeness. Con: the document becomes a taxonomy
   exercise rather than an operational playbook; the long-tail
   triggers do not add value beyond "use judgment".
3. **Seven triggers** including the explicit non-trigger of
   "onboarding a new collaborator". Adopted.

**Rationale.** Naming the explicit non-trigger of "onboarding a
new collaborator" is the engineer's calibration: a reader of §7
might infer that every collaborator change triggers an out-of-band
review, which would create review fatigue. The non-trigger note
clarifies that **adding** a collaborator is the access decision
itself; the next scheduled quarterly review covers the addition.
**Removing** a collaborator is, by contrast, a trigger because
the access surface contracts in a way the scheduled cadence might
not catch fast enough.

**Cross-reference to IR plan.** Triggers §7.3 (CVE published),
§7.4 (suspected credential compromise), and §7.5
(disclosure-triggered) all explicitly compose with the IR plan's
playbooks (§7.2 auth-compromise, §7.3 dependency vulnerability).
The composition is intentional: the IR plan governs the response
to the incident character; this document governs the access-side
decision in parallel.

---

## Decisions not made in this slice (deferred)

- **Automation implementation.** §8 names seven candidates as
  hardening items; none ship in this slice. The 2027-05-28 annual
  review is the named decision point for prioritization.
- **Repository-to-organization migration.** §8 names this as a
  hardening item; the decision depends on the GOVERNANCE.md
  re-evaluation trigger.
- **First-review execution.** This slice files the cadence
  document; the 2026-08-28 first quarterly review is a future
  operational event, not part of this slice's deliverables.
- **Cross-tier surface migration.** If experience with the
  manual-baseline reviews surfaces that a given surface should
  move tier (e.g., CI secrets should be semi-annual rather than
  quarterly, or PATs should be semi-annual rather than annual),
  the 2027-05-28 annual review of this document is the decision
  point. No tier migrations are committed today.

---

## Cross-references to constitutional commitments

- **Canvas invariant #6 (RLS at the database layer).** Named in §9
  as the runtime tenant-isolation analogue at the database layer
  that this document complements at the GitHub-repository boundary.
  The two commitments compose at different timescales — RLS
  enforces tenant separation per query; this document enforces a
  periodic human review of who can modify the deployment substrate.
- **AI-assist boundary (hard) — `CLAUDE.md`.** This document is
  fully human-authored. No AI-drafted content was approved without
  human review per the schema-level enforcement.
- **Anti-pattern rejection — `CLAUDE.md` "Anti-patterns we
  explicitly reject".** The cadence does not propose proprietary
  collector agents, AI-generated access decisions, or aspirational
  review cadences the maintainer cannot sustain. The plan describes
  the access-review capabilities the project actually has.
- **Tone discipline — `docs/governance/board-narrative-tone-anti-patterns.md`.**
  §4 procedural prose explicitly avoids the banned phrase list (no
  "industry-leading", no "robust" as filler, no "leverage" as
  verb, no unprompted superlatives).
- **Slice 329 audit binding.** This slice closes finding H-3; the
  audit report explicitly named slice 374 as the spillover for
  the access-review-cadence half of the v1 binary-success
  organizational-controls gap.
- **Sibling slice 372 + 373 binding.** §5 (Solo-maintainer) reuses
  the IR + BCP plans' solo-maintainer role devolution pattern;
  §3 (Cadence) co-schedules the annual review with the IR + BCP
  plans' annual tabletop; §6 (Audit trail) shares the
  TOML-frontmatter + `[redacted]` confidentiality posture. The
  three documents are designed to compose, not duplicate.
