# Governance

This document records how security-atlas is governed. It is the canonical
companion to [`LICENSE`](./LICENSE) (Apache 2.0) and
[`CONTRIBUTING.md`](./CONTRIBUTING.md) (DCO sign-off discipline). Where the
license says what you can do with the code, this document says how the
project itself is run, who decides what, and what the maintainer has
committed to NOT do.

It is short by design. Concrete commitments matter more than comprehensive
process.

Companion canvas reference: [`Plans/canvas/11-open-questions.md`](./Plans/canvas/11-open-questions.md)
open question #5 (resolved 2026-05-20). This document is the visibility
commitment that resolution identified as the load-bearing follow-on work.

---

## Model

security-atlas is a **pure-community open-source project** under the
[Apache 2.0 license](./LICENSE).

- There is **no hosted SaaS** offered by the project owners.
- There is **no enterprise edition** with proprietary features.
- There is **no dual-license** path. Apache 2.0 is the only license; the
  project is not relicensable without consent of every contributor on
  record.
- There are **no closed-source plugins, connectors, or modules**. Every
  piece of the platform is in this repo under the same license.

This posture is **time-bounded** — see [Re-evaluation trigger](#re-evaluation-trigger)
below. The maintainer reserves the right to revisit the model at the named
trigger; until then, this is the model.

The reason for the posture: at pre-product-market-fit scale, committing
engineering capacity to billing, multi-tenant SaaS operations, or enterprise
sales is premature. The credible target user — a security-conscious solo
operator at a 50-150-person startup — wants something they can self-host
without vendor lock-in. Pure-community OSS is the strongest version of
that story.

---

## Maintainership

security-atlas is currently maintained by **Matt Goodrich**
([@mgoodric](https://github.com/mgoodric)) as the **benevolent dictator for
life** (BDFL) of the v1 product direction.

This is solo maintainership, not a council. It is explicitly named here
because adopters deserve to know who is on the hook.

### Path to co-maintainership

The BDFL model evolves into a small **advisory council** when ALL of the
following hold:

1. There are **at least three (≥ 3) active outside contributors** with
   merged work on `main`.
2. Each of those contributors has **at least six (≥ 6) months** of
   sustained involvement (commits, reviews, issue triage — not a single
   drive-by PR).
3. The maintainer judges that the contributors' design instincts have
   converged enough that they can co-decide architectural questions
   without thrashing.

Until that bar is met, the project is BDFL. Promotion to advisory council
is a one-way ratchet — once formed, the council does not dissolve back to
BDFL on a single departure.

### Current admin/maintain rights

The accounts with `Admin` or `Maintain` role on the
[`mgoodric/security-atlas`](https://github.com/mgoodric/security-atlas)
GitHub repository are:

- [@mgoodric](https://github.com/mgoodric) (Admin) — Matt Goodrich, sole
  maintainer

That is the exhaustive list. No bots, no shared accounts, no organization
teams with elevated rights.

---

## Decision-making

Decisions follow the project's existing canvas + slice + ADR discipline.
Nothing in this document introduces a new process surface; it just names
the one already in place.

| Decision type                               | Where it lives                                                                       | Who decides                          |
| ------------------------------------------- | ------------------------------------------------------------------------------------ | ------------------------------------ |
| Product direction (what to build, what not) | [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) and `Plans/canvas/` | BDFL                                 |
| Routine product work (per-slice scope)      | [`docs/issues/<NNN>-*.md`](./docs/issues/) slice docs                                | BDFL; contributors via PR            |
| Major architectural pivots                  | [`docs/adr/<NNNN>-*.md`](./docs/adr/) Architectural Decision Records                 | BDFL with public ADR + canvas update |
| Slice-time JUDGMENT calls (UX copy, etc.)   | `docs/audit-log/<NNN>-*-decisions.md` per slice                                      | Implementing contributor             |
| Tooling / housekeeping                      | PR + review                                                                          | Any maintainer                       |
| Security disclosures                        | [`SECURITY.md`](./SECURITY.md) private channel                                       | BDFL                                 |

**Major architectural pivots require an ADR.** The ADR captures the
trade-off; the canvas captures the resolved invariant; the slice ships
the work. The three artifacts are kept consistent across one merge.

**Routine product work** flows through the slice pipeline
([`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) for the backlog,
[`docs/issues/_STATUS.md`](./docs/issues/_STATUS.md) for the live merge
trail). Contributors are welcome to file slices; the maintainer either
accepts or counters with scope edits.

**Conflict resolution.** Disagreement on a PR is resolved by discussion in
the PR thread, escalating to a separate issue if the question outgrows the
PR. The BDFL has the final call; the call is recorded in the issue or in
an ADR if the decision is architecturally load-bearing.

---

## Contributor agreements

security-atlas uses the
[Developer Certificate of Origin (DCO)](https://developercertificate.org/)
— **there is no Contributor License Agreement (CLA)**.

DCO sign-off is sufficient for Apache 2.0 hygiene. A CLA would add friction
without adding meaningful legal protection that the DCO does not already
provide. The maintainer commits to NOT introducing a CLA.

Every commit MUST carry a `Signed-off-by:` trailer
(`git commit -s`). CI rejects PRs whose commits lack a sign-off. See
[`CONTRIBUTING.md`](./CONTRIBUTING.md) §"Developer Certificate of Origin (DCO)"
for the full text and `Co-authored-by:` trailer convention for AI-assisted
commits.

---

## Funding posture

security-atlas accepts funding through **documented sponsorship channels
only**. Funding does not buy roadmap influence; it underwrites maintainer
time and infrastructure (CI minutes, security scanning, domain renewals,
etc.).

**Active channels:**

- **GitHub Sponsors** — visible on the repo's GitHub UI via the "Sponsor"
  button (configured in [`.github/FUNDING.yml`](./.github/FUNDING.yml)).
  Set up but **not marketed** — the README does NOT prominently link to
  it. Adopters find it via the GitHub UI on their own time.
- **Corporate sponsorship inquiries** — open an issue with the
  `funding-discussion` label. The conversation happens in the open; the
  outcome (accepted / declined / structure) is recorded in the issue.
  Named-tier sponsorship structures ("Bronze / Silver / Gold") are NOT
  pre-designed; tiers emerge from real conversations if they emerge at
  all.

**Out of scope:**

- The maintainer's separate consulting, conference-speaking, and writing
  income is **entirely outside the project's scope**. The project does
  not invoice clients; it does not employ anyone; it does not own a bank
  account. Maintainer-funded infrastructure (e.g., a personal credit card
  for a domain name) is donated, not reimbursed, and is named in
  [`docs/audit-log/`](./docs/audit-log/) when it occurs.
- Pay-to-merge, pay-to-prioritize, and any other arrangement that
  exchanges money for roadmap movement are not offered.
- Bug-bounty programs are not offered at this time. Security disclosures
  go through [`SECURITY.md`](./SECURITY.md).

**Anti-commitment:** the maintainer commits to NOT building a fundraising
machine on top of the project. Funding signals exist so that an enterprise
adopter who WANTS to fund the work has a path; they do not exist to
solicit donations.

---

## Re-evaluation trigger

The pure-community-OSS posture is **time-bounded**. The maintainer will
formally re-evaluate the model when **either** of the following fires,
whichever comes first:

- **Date trigger:** **2028-05-20** (two years from the OQ #5 resolution
  date of 2026-05-20).
- **Adoption trigger:** **100 deployed self-hosts**, proxied by GitHub
  release-download statistics. (No in-product telemetry exists; release-
  download count is the explicit proxy. The proxy is imperfect but
  honest — a self-host count cannot be measured without telemetry, and
  the maintainer has committed to NOT introducing telemetry. See
  [Quarterly checkin](#quarterly-checkin) below for how the proxy is
  tracked.)

At the trigger, the maintainer evaluates four options against the
then-current product surface and community signal:

- **Option A — stay pure community OSS.** Status quo. Re-affirm and
  set the next trigger.
- **Option B — introduce a hosted SaaS** offered by the project owners
  alongside the open-source self-host. License unchanged.
- **Option C — introduce an enterprise edition** with proprietary
  features. License changes implications carefully evaluated.
- **Option D — transfer the project to an OSS foundation** (e.g., CNCF
  sandbox / Linux Foundation / Apache Software Foundation) for
  longer-term vendor-neutral stewardship.

The evaluation is recorded as a new ADR under `docs/adr/` + a canvas
resolution update at `Plans/canvas/11-open-questions.md` #5. The
evaluation is **not** silent; it is a public-record event.

**Why the date AND the metric:** a pure date trigger creates surprise
re-evaluation right when the project might be at a critical adoption
moment. A pure metric trigger may never fire if growth stalls. Both
together force the re-evaluation conversation on a known schedule but
allow it to happen earlier if real adoption warrants.

### Quarterly checkin

The maintainer records a **quarterly governance checkin** at
`docs/audit-log/governance-checkin-<YYYY>-Q<N>.md`. The checkin captures
the current state of the re-evaluation trigger metrics so that the
2028-05-20 conversation is informed by trend data, not a single snapshot.

**Template fields (every checkin includes all five):**

1. **Cumulative GitHub release-download count + delta this quarter** —
   the self-host proxy. Reported as `<total>` / `+<delta>` since the
   prior checkin. Baseline N = 0 at the first checkin if no releases
   have happened yet.
2. **GitHub stars delta this quarter** — social signal, less load-bearing
   than downloads.
3. **New contributor count this quarter** — number of distinct GitHub
   accounts that landed their first PR this quarter (counts toward the
   "≥ 3 active outside contributors" advisory-council formation bar).
4. **Maintainer assessment "trigger fired?"** — `Y` or `N` with a one-
   sentence rationale. The trigger fires when the maintainer judges that
   the date OR the metric has crossed.
5. **Anything else worth flagging** — funding signals received, major
   regulatory shifts affecting the GRC space, bus-factor changes,
   anything noteworthy.

The first checkin (`governance-checkin-2026-Q2.md`) is committed in the
same change as this document, establishing the baseline.

---

## Bus-factor & succession

security-atlas currently has a **single primary maintainer**. This is the
bus-factor problem stated plainly, not softened.

If the maintainer becomes unable to maintain the project — incapacitation,
prolonged absence, loss of interest — the following is what continues to
exist and what does not:

- **What continues.** The project's
  [Apache 2.0 license](./LICENSE) and the immutability of git history
  ensure the code remains available **forever**. Downstream forks may
  continue development on their own cadence. Adopters who self-host
  remain unaffected operationally — the deployed binary keeps running;
  there is no phone-home; there is no license server.
- **What pauses.** Active development on `github.com/mgoodric/security-atlas`
  pauses until a successor maintainer is identified. Releases, security
  patches, dependency bumps, and roadmap movement on this specific repo
  stop. Issues stay open; PRs stay un-reviewed.
- **What does not exist.** There is no escrow, no foundation, no
  pre-arranged successor. If the maintainer is hit by a bus today,
  there is no one with admin rights to transfer ownership.

This is honest, not aspirational. **Reducing this risk is an explicit
Year 1 priority.** The maintainer commits to:

- **Recruit at least one co-maintainer by 2027-05-20** (one year from
  the OQ #5 resolution date). This is recruitment work that happens
  out-of-band — it is not a slice and cannot be solved by a PR. The
  maintainer will surface a co-maintainer candidate through the
  contributor flywheel (sustained PRs + reviews + design conversations).
- **Document the GitHub-org-transfer recovery path** in a sealed
  envelope held with a personal trusted contact, so that if the
  maintainer disappears without notice the project can be moved to a
  successor without legal entanglement.
- **Report bus-factor state in every quarterly checkin** (see field 5
  above).

The 2027-05-20 recruitment target is a year before the 2028-05-20
re-evaluation trigger by design: arriving at the re-evaluation
conversation with a single maintainer is a strictly worse position than
arriving with two.

---

## What this document does NOT do

- It does **not** replace [`LICENSE`](./LICENSE),
  [`CONTRIBUTING.md`](./CONTRIBUTING.md), [`SECURITY.md`](./SECURITY.md),
  or [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md). It complements them.
- It does **not** promise features. The slice backlog
  ([`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md)) is where roadmap
  conversations live.
- It does **not** lock in 2028-05-20 as the date the model changes —
  only as the date the model is re-evaluated. The outcome of that
  evaluation may well be "stay pure community OSS, set new trigger".
- It does **not** introduce telemetry. The maintainer commits to NOT
  baking in self-host phone-home; the release-download proxy is the
  full extent of measurement.

---

## Document maintenance

Changes to this document require a PR, a maintainer's approval, and a
DCO-signed commit like every other change in the repo. Material changes
(model evolution at the re-evaluation trigger, contributor-agreement
shifts, succession-plan updates) MUST also be recorded as an ADR under
`docs/adr/` and a canvas resolution update so the change shows up in the
project's structured decision trail.

Filed 2026-05-20 as the OQ #5 visibility commitment (slice 181).
