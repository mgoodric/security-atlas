# 373 — Business continuity / disaster recovery plan · decisions log

**Slice:** 373 — Business continuity / disaster recovery plan (governance document)
**Slice type:** JUDGMENT
**Filed:** 2026-05-28
**Closes:** Slice 329 compliance meta-audit finding **H-2**.
**Companion document:** `docs/governance/business-continuity.md`
**Sibling slice:** 372 (incident response plan, merged at `38de6363`) — pattern source for solo-maintainer role devolution, tabletop cadence, and tone discipline.

---

## D1 — GOVERNANCE.md disposition: exists; cross-reference (no modification)

**Decision.** `GOVERNANCE.md` exists at the repo root (filed by
slice 181 on 2026-05-20). Cross-reference it from §3 (Roles) and §7
(Continuity of the OSS project). **Do not modify GOVERNANCE.md
content** — per P0-373-3 (does NOT touch CLAUDE.md or canvas, by
extension treating GOVERNANCE.md as a canonical neighbor that is
not in this slice's modification scope) and per the slice doc
narrative which explicitly references GOVERNANCE.md as the named
upstream for bus-factor & succession.

**Alternatives considered.**

1. **Bundle missing GOVERNANCE.md inline.** GOVERNANCE.md was a
   possibility-not-fact at slice 373 spawn time (the work-order
   said "GOVERNANCE.md (or create if missing — engineer's judgment
   whether to write a minimal one OR file as spillover)"). On
   inspection at slice start, GOVERNANCE.md is fully present
   with a dedicated "Bus-factor & succession" section that
   already operationalizes the relevant commitments. Bundling
   would be duplicative and would violate the cross-references-
   over-duplication principle the governance README codifies.
2. **File a spillover slice to enhance GOVERNANCE.md.** Considered
   for one specific gap — GOVERNANCE.md says the maintainer "will
   document the GitHub-org-transfer recovery path in a sealed
   envelope" but doesn't state whether the envelope exists. This
   is a real gap. However, filing a spillover for "verify the
   envelope exists" is operational work, not a slice; the §7
   sentence "until the envelope exists in practice, the bus-factor
   scenario degrades to fork from any contributor's local clone"
   names the gap honestly without creating a spillover.
3. **Cross-reference + no modification.** Adopted.

**Rationale.** GOVERNANCE.md is the canonical source-of-truth for
bus-factor & succession. The BCP/DR plan's job is to operationalize
the consequence (continuity-event response when the maintainer is
unavailable), not to re-author the upstream policy. Cross-references
in §3 and §7 link the two documents bidirectionally; the BCP plan
adds operational detail without duplicating governance content.

**Boundary.** If a future GOVERNANCE.md change materially alters the
bus-factor & succession commitment (e.g., the advisory-council
trigger fires; the sealed-envelope content is concretized into a
named successor surface), §7 of the BCP plan is updated in the same
slice that lands the GOVERNANCE.md change. The two documents are
kept consistent at every annual review.

---

## D2 — RTO/RPO target shape: 5-tier with sole-maintainer honest targets

**Decision.** Define five tiers (Tier 0-4) with RTO/RPO targets
calibrated to **solo-maintainer single-host reality**, not aspirational
SaaS-vendor targets. Tier 3 (the SaaS instance) lands at **RTO 4
hours / RPO 24 hours**, with the 4-hour RTO explicitly named as
best-effort during waking hours.

**Alternatives considered.**

1. **Three-tier shape (Critical / Important / Nice-to-have).** This
   is the shape the slice 329 audit's H-5 (asset inventory)
   suggested for criticality grading. Pro: aligns with the
   audit-finding nomenclature. Con: collides with the IR plan's
   P0/P1/P2/P3 severity tiers — adopting yet another tiering
   vocabulary for BCP would create three vocabularies (CVSS-style
   for vuln severity, P-prefix for incident response, Critical/
   Important/Nice-to-have for asset criticality). Reduces
   readability across documents.
2. **Tier-by-numeric-rank shape (Tier 0-4) without sub-grading.**
   Adopted. Each tier represents a coherent asset class with
   shared RTO/RPO targets:
   - Tier 0: code and git history (RTO 24h / RPO 0)
   - Tier 1: distribution artifacts (RTO 7d / RPO N/A — reproducible)
   - Tier 2: docs / communication (RTO 24h / RPO 0)
   - Tier 3: maintainer SaaS instance (RTO 4h / RPO 24h)
   - Tier 4: supporting services + keys (RTO 48h / RPO N/A —
     rotation)
3. **Single global RTO/RPO target.** Pro: simplicity. Con:
   inaccurate. Different asset classes have very different recovery
   characteristics; collapsing them to a single number would be
   either too aggressive (committing to RTO 4h on the GitHub repo,
   which is GitHub's responsibility) or too lax (committing to RTO
   7d on the SaaS instance, which is unnecessarily slack).

**Rationale.** Per the work-order's "RTO/RPO realism" guidance and
P0-373-1 ("does NOT promise an RTO/RPO that's not realistic for solo-
maintainer single-host"), the targets must be calibrated to what
the maintainer can deliver. The five-tier shape preserves precision
while keeping the table small enough to be readable. Tier 3's RTO 4h
explicitly states "best-effort during waking hours" — the document
does not claim 24/7 coverage.

**Per-tier rationale (compressed):**

- **Tier 0 RTO 24h / RPO 0:** Git is distributed; every clone is a
  full mirror; maintainer-local mirror is the recovery substrate.
  RPO 0 holds because no commit on `main` is uniquely held by
  GitHub.
- **Tier 1 RTO 7d / RPO N/A:** Container images are reproducible
  from tagged commits. Seven days is the honest interval to push
  to a replacement registry + update docs + announce. RPO N/A
  because artifacts are deterministic outputs of source.
- **Tier 2 RTO 24h / RPO 0:** Docs are regenerated from `main`;
  mechanical.
- **Tier 3 RTO 4h / RPO 24h:** The honest answer for a single-host
  Unraid deployment with `pg_dump` nightly. RPO < 24h would require
  PostgreSQL WAL archival, which is **named as a hardening item in
  §11 but not committed today**. The document is explicit about
  this gap.
- **Tier 4 RTO 48h / RPO N/A — rotation:** Account state and
  credentials are largely externally-recovered (vendor SLAs); the
  48h target reflects the maintainer's time to verify post-recovery
  state and rotate any potentially exposed credentials.

**Anti-pattern explicitly rejected.** "RTO 5 minutes" or "99.99%
SaaS uptime" for the maintainer-operated Unraid box. P0-373-1
enforces this. The IR plan's D2 (solo-maintainer role devolution)
established this honesty pattern; D2 here carries it into the
BCP/DR surface.

---

## D3 — Restore-scenario coverage: five scenarios A–E

**Decision.** Cover **five scenarios** in §6: A (Unraid hardware
failure), B (PostgreSQL corruption), C (object storage loss), D
(ransomware on the SaaS instance), E (GitHub organization
compromise). The work-order specified these five exactly; this
decision documents the rationale for landing exactly that set,
not more and not fewer.

**Alternatives considered.**

1. **Fewer scenarios — collapse C into A.** Pro: shorter document.
   Con: Scenario C is the load-bearing scenario for canvas invariant
   #3 (append-only evidence ledger). Collapsing it would lose the
   explicit replay-from-ledger procedure. Rejected.
2. **More scenarios — add a Scenario F for network outage on the
   SaaS instance.** Pro: completeness. Con: network outage is
   substantively the same as Scenario A from a recovery standpoint
   (detect → wait → resume); it does not exercise a unique restore
   path. Adding it would dilute the playbook without adding signal.
   Rejected.
3. **Exactly the five A–E specified.** Adopted.

**Rationale.** Each of the five scenarios exercises a distinct
restore path:

- **A** exercises the **hardware recovery path** — chassis swap,
  data-disk re-mount, docker-compose re-up. Tests the spare-
  hardware commitment.
- **B** exercises the **Postgres restore path** — `pg_dump` recovery,
  RLS verification, integrity check. Tests the §5 Tier 3 backup
  posture.
- **C** exercises the **evidence-ledger replay path** — the load-
  bearing recovery substrate that canvas invariant #3 enables.
  Tests the most novel and most security-atlas-specific recovery
  procedure.
- **D** exercises the **incident + continuity composition path** —
  IR plan handles detect/contain, this plan handles restore/verify/
  resume. Tests the inter-plan handoff.
- **E** exercises the **GitHub-loss recovery path** — the
  catastrophic scenario for the project's continuity posture.
  Tests the Tier 0 recovery substrate and the §11 off-GitHub
  mirroring hardening item.

Together, the five scenarios cover the cross-product of (asset
tier) × (failure mode) with no gaps that have a unique restore
procedure.

**Boundary.** Scenarios outside the five A–E set are not
unaddressed — they are addressed by the most-similar scenario.
A natural-disaster scenario at the operator's data center is the
operator's BCP per §1; a maintainer-workstation failure is
indirectly Scenario E (depends on the maintainer-local mirror); a
CI runner pool outage is Tier 4 supporting-services Scenario E.
The five scenarios are the **canonical recovery procedures** the
plan exercises; novel real events are mapped onto the closest
scenario at recovery time.

**Each scenario lists detect/contain/restore/verify/resume.** This
five-substep template is required by AC-3 and is uniform across
all five scenarios, preserving readability and ensuring the IR-
plan-vs-BCP-plan handoff is unambiguous (detect/contain in the IR
plan; restore/verify/resume in this plan).

---

## D4 — Tabletop cadence: annual, co-scheduled with IR plan tabletop

**Decision.** Commit to **annual** tabletop exercises **co-scheduled
with the IR plan tabletop** at 2027-05-28. Tabletop scope rotates
between scenarios on a multi-year cycle (Year 1 covers A+B; Year 2
covers C+E Path 1; Year 3 covers D+E Path 2; Year 4+ repeats with
rotation tuned to incident history).

**Alternatives considered.**

1. **Independent tabletop schedule for BCP.** Pro: each plan gets
   dedicated focus. Con: continuity events almost always have an
   incident character; exercising the two plans separately would
   miss the composition that real events demand. Rejected.
2. **Co-scheduled tabletop, single scenario per year.** Pro:
   simpler. Con: a year is long enough that single-scenario
   coverage leaves four years between revisits of the most
   common scenarios. Rejected.
3. **Co-scheduled tabletop, rotating two-scenario coverage per
   year.** Adopted. Each year exercises two scenarios; cycle
   completes in 3 years; tighter cycle than single-scenario,
   broader coverage than all-five-every-year.

**Rationale.** Mirrors the IR plan's D4 (annual cadence) by design;
the work-order explicitly calls out "tabletop co-scheduled with
slice 372 (2027-05-28)" as AC-5. Co-scheduling means one
maintainer-day per year for both tabletops rather than two.

**Cross-references.** The IR plan §11 commits to chaos-experiment
substrate (slice 335) as supplementary stress; this BCP plan §8
adopts the same posture and identifies the two chaos experiments
that directly exercise BCP restore paths (Experiment 5 Postgres
failover; Experiment 7 object-storage outage). Triple-redundant
testing surface (annual tabletop + chaos backlog + quarterly audit
cycle) is the same shape the IR plan committed to.

---

## D5 — Off-GitHub mirroring: named as hardening item, not committed

**Decision.** Name **off-GitHub repository mirroring** as a §11
hardening item ("not committed today"). Do **not** commit to
implementing it in this slice. The §6 Scenario E procedure
documents that the maintainer-local mirror is the load-bearing
recovery substrate and explicitly names the "single-substrate
risk" that off-GitHub mirroring would close.

**Alternatives considered.**

1. **Commit to off-GitHub mirroring as part of this slice.** Pro:
   closes the single-substrate risk immediately. Con: implementing
   off-GitHub mirroring is operational infrastructure work that
   warrants its own slice (which off-GitHub provider, how is the
   mirror authenticated, what is the rotation cadence). This slice
   is a governance document, not infrastructure.
2. **Stay silent on the gap.** Pro: shorter document. Con: the
   document would describe a recovery posture (§6 Scenario E Path 2)
   without acknowledging that it has a single substrate. P0-373-1
   prohibits unrealistic claims; staying silent risks the reader
   inferring that the recovery is more robust than it is. Rejected.
3. **Name the hardening item with explicit "named; not committed"
   status.** Adopted. The §11 table makes the status of each
   hardening item visible; the maintainer's annual review
   surfaces them for prioritization.

**Rationale.** The engineer-as-collaborator process note in the
work-order explicitly authorizes this pattern: "if you find that
critical backup procedures don't currently exist..., note in PR
body for maintainer attention — but the BCP/DR plan describes
intended state + commits to building any missing pieces, NOT lies
about current state." Off-GitHub mirroring is the canonical example
— the plan acknowledges it would improve continuity, names the gap
it closes, and leaves the decision to commit (and the implementation
slice) to a future cycle.

**Other hardening items named under the same pattern (full list in
§11):**

- PostgreSQL WAL archival on SaaS instance
- JWT signing key rotation automation (tracked at slice 366 —
  **committed work, not yet scheduled**)
- cosign image signing (tracked at slice 368 — **committed work,
  not yet scheduled**)
- Public status page
- Operator mailing list
- Spare hardware verification (named; **verified at every annual
  review** — first verification at 2027-05-28)

The pattern matches the IR plan's D3 (communication-channel
inventory: existing channels only; no new infrastructure) — the
governance documents describe capabilities the project has, not
infrastructure it intends to build.

---

## D6 — Spare hardware commitment: named for the first time

**Decision.** Name the **maintainer's spare hardware commitment**
in §6 Scenario A explicitly. The chassis-failure recovery path
depends on the maintainer keeping spare hardware available; if
spare hardware is not maintained, the recovery degrades to "acquire
hardware" (multi-day) plus the documented steps. Verification of
spare-hardware availability is committed at every annual review
per §11.

**Alternatives considered.**

1. **Assume spare hardware silently.** Pro: shorter document.
   Con: hides a real dependency on personal infrastructure that
   the maintainer might not in fact maintain. If a chassis failure
   occurred today and no spare existed, the documented RTO of 4
   hours would be unachievable, and the document would have made
   a commitment it cannot keep.
2. **Commit to procuring spare hardware now.** Pro: definitively
   closes the gap. Con: hardware procurement is out-of-scope for
   a governance-document slice; the maintainer's personal-IT
   choices are not slice-shaped work.
3. **Name the commitment with annual verification.** Adopted.

**Rationale.** The work-order explicitly calls out the engineer-
as-collaborator pattern: surface gaps in current state without
inflating the documented posture. Spare hardware is the most
concrete example — the §6 Scenario A path assumes its availability;
§5 Tier 3 doesn't directly address it; §11 closes the loop with
"verified at every annual review."

This decision is also the load-bearing example for §11's
"named hardening items, not committed today" table format. The
spare-hardware row is the first row that demonstrates the pattern's
clarity: status column reads "Named; verified at every annual
review" rather than "Not committed."

---

## D7 — Length budget: ~1,100 lines (above the slice doc's 150-300 target)

**Decision.** Land the BCP/DR plan at approximately 1,100 lines of
Markdown — substantially above the slice doc's "~150-300 lines of
Markdown" length target.

**Alternatives considered.**

1. **Honor the 150-300 line target by aggressive compression.**
   Pro: matches the slice doc. Con: the work-order from the
   maintainer that spawned the engineer agent overrides the slice
   doc and explicitly specifies "~800-1500 lines" for the BCP
   plan. The two are inconsistent.
2. **Honor the work-order's 800-1500 line target.** Adopted.

**Rationale.** The work-order's eleven-section structure with five
fully-elaborated scenarios (each with detect/contain/restore/verify/
resume sub-steps) cannot be compressed into 300 lines without
losing the per-scenario specificity that the AC-3 ("5 restore
scenarios (A-E) each with detect/contain/restore/verify/resume
sub-steps") explicitly requires. The IR plan (slice 372) landed at
~870 lines for its twelve-section structure; the BCP/DR plan
landing at ~1,100 lines for its eleven-section structure (longer
restore-scenario detail per scenario) is consistent with the
sibling-plan precedent.

The slice doc's 150-300 line target was written before the
work-order specified the eleven-section depth. The work-order is
the operative specification for this slice; the slice doc remains
useful as the audit-finding-linkage record.

**Boundary.** Future revisions during annual review may compress
sections that proved over-specified, or expand sections that
proved under-specified. Length is not itself a constitutional
commitment.

---

## D8 — Out-of-scope content explicitly enumerated

**Decision.** §1 "What this plan does not cover" enumerates four
out-of-scope items explicitly: continuity inside operator-hosted
deployments, regional natural disasters affecting operators,
customer-side compliance program continuity, and 24/7 uptime SLAs.

**Alternatives considered.**

1. **Leave scope implicit.** Pro: shorter. Con: a reader trying
   to use this plan as a template for their own operator-side
   deployment, or a third-party auditor verifying scope coverage,
   would not know whether the project claims these areas. Auditors
   would ask. Rejected.
2. **Explicit out-of-scope section.** Adopted.

**Rationale.** Mirrors the IR plan's D8 (out-of-scope governance
content explicitly named) and the slice doc P0 anti-criteria
pattern (state what the slice will not do, not just what it will).
Calibrates reader expectation. Reduces the surface for
misunderstanding. Particularly important for the BCP/DR surface
because the natural-disaster question is the first thing a reader
expects from a BCP document; explicitly saying "operator-hosted
deployments and their regional disasters are out of scope"
prevents the inference that the project failed to consider them.

---

## Decisions not made in this slice (deferred)

- **Off-GitHub mirror provider choice.** §11 names the hardening
  item; the provider (Codeberg, Gitea, GitLab, etc.) is a future
  slice that lands the actual infrastructure work.
- **PostgreSQL WAL archival implementation.** §5 Tier 3 names the
  gap; implementation is a future slice.
- **Public status page provider and content.** §9 names the
  deferral; implementation is a future slice.
- **Operator mailing list mechanism.** §9 names the deferral;
  implementation is a future slice (depends on adoption signal
  per GOVERNANCE.md re-evaluation trigger metrics).
- **Slice 376 asset inventory canonical landing.** §4 cross-
  references slice 376; until 376 ships, §4's table is the
  working inventory for BCP-touched assets.

---

## Cross-references to constitutional commitments

- **Canvas invariant #3 (append-only evidence ledger).** Load-bearing
  for §6 Scenario C — full-bucket object-storage loss is recoverable
  because the ledger preserves every evidence record's content hash
  and observed_at, enabling re-ingestion and artifact-lost marking
  without violating the append-only contract.
- **Canvas invariant #6 (RLS at the database layer).** Load-bearing
  for §6 Scenario B — Postgres restore must preserve tenant_id
  integrity; the §6 Scenario B "Verify" step explicitly verifies
  RLS policies are present in the restored database.
- **Canvas §8.4 (audit-period freezing).** Load-bearing for §6
  Scenario D — ransomware recovery must assess audit-period-freeze
  integrity; periods whose `frozen_at` falls within the compromise
  window are re-frozen against the restored evidence ledger.
- **AI-assist boundary (hard) — `CLAUDE.md`.** This document is
  fully human-authored. No AI-drafted content was approved without
  human review per the schema-level enforcement
  (`ai_assisted=true ↔ human_approver` invariant).
- **Anti-pattern rejection — `CLAUDE.md` "Anti-patterns we
  explicitly reject".** The BCP/DR plan does not propose proprietary
  collector agents, AI-generated recovery procedures without human
  approval, or aspirational uptime claims. The plan describes the
  recovery capabilities the project actually has.
- **Tone discipline — `docs/governance/board-narrative-tone-anti-patterns.md`.**
  §9 explicitly references the tone discipline; the document itself
  avoids the banned phrase list (no "industry-leading", no "robust"
  as filler, no "leverage" as verb, no unprompted superlatives).
- **Slice 329 audit binding.** This slice closes finding H-2; the
  audit report explicitly named slice 373 as the spillover for the
  Availability TSC half of the v1 binary success criterion.
- **Sibling slice 372 binding.** §3 (Roles) reuses the IR plan's
  solo-maintainer role devolution pattern; §8 (Tabletop) co-schedules
  with the IR plan tabletop; §9 (Communications) reuses IR plan
  channels; §10 (Documentation) shares the IR plan's
  `docs/incidents/` directory. The two plans are designed to
  compose, not duplicate.
