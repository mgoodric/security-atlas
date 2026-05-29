# 375 — Data retention and disposal policy · decisions log

**Slice:** 375 — Data retention and disposal policy (governance document)
**Slice type:** JUDGMENT
**Filed:** 2026-05-28
**Closes:** Slice 329 compliance meta-audit finding **H-4**.
**Companion document:** `docs/governance/data-retention.md`
**Sibling slices:** 372 (incident response plan, merged at `38de6363`), 373 (business continuity plan, merged at `a84da085`), and 374 (access review cadence, merged at `257853ea`) — pattern sources for solo-maintainer role devolution, annual co-scheduled review, tone discipline, enforcement-gap honest acknowledgment, and engineer-as-collaborator gap-naming.

---

## D1 — Filename: `data-retention.md` (not `data-retention-disposal-policy.md`)

**Decision.** Ship the document at `docs/governance/data-retention.md`,
following the sibling naming convention. The slice doc and the
governance README's "Planned documents" table referred to
`docs/governance/data-retention-disposal-policy.md`; this slice
adopts the shorter form for consistency with the existing files.

**Alternatives considered.**

1. **`data-retention-disposal-policy.md`** as written in the slice doc
   and the sibling README's planned-documents table. Pro: matches
   the planned-documents row verbatim. Con: inconsistent with sibling
   docs `incident-response.md`, `business-continuity.md`,
   `access-review.md` — none carry the redundant `-plan` /
   `-disposal-policy` /`-cadence` suffix.
2. **`data-retention-policy.md`.** Pro: explicit about policy nature.
   Con: introduces a third naming convention; mismatches sibling
   pattern.
3. **`data-retention.md`** — adopted. Matches sibling convention
   exactly per slice 374 D1 precedent.

**Rationale.** Cross-references-over-duplication and pattern-matching
are explicit principles in the governance README's "Conventions"
section. The cost of moving the slice doc + governance README
planned-row to match (cheap) is materially smaller than the cost of
a permanent naming-pattern inconsistency among four sibling
governance docs (paid forever in reader cognitive load). Sibling
slice 374 made the identical decision at D1 and the same logic
applies here.

**Boundary.** The governance README's "Planned documents" row is
updated to remove slice 375 (now Current); the path for slice 376
is left unchanged.

---

## D2 — Scope honesty: project-self only, not operator-side, not tenant-data

**Decision.** Scope this document **strictly to the project's own
data** (categories §2.1-§2.7 in the policy) — source code, governance
corpus, audit-trail artifacts, CI/CD artifacts, maintainer-operated
SaaS instance state, third-party-service state, issue-tracker state.
**Operator-side retention** (operators self-hosting security-atlas
choose their own retention policy) and **tenant-data retention inside
operator deployments** (governed by the contract between the operator
and their customers) are **explicitly out of scope** per the §1
"What this policy does not cover" enumeration.

**Alternatives considered.**

1. **Document operator-side AND tenant-data retention as binding
   project commitments.** Pro: more comprehensive; matches the
   work-order's broader inventory list (audit logs / evidence ledger
   / board narrative drafts / CI/CD logs / observability data).
   Con: violates P0-375-1 — those retentions are NOT enforced by
   the project for operator deployments (each operator runs their
   own deployment with their own retention configuration). Claiming
   "7-year audit log retention" as a project commitment when in fact
   it is an operator-configurable knob would make the document
   false on its face. Rejected.
2. **Document project-self AND operator-side, but mark operator-side
   sections as "guidance not commitment".** Pro: gives operators a
   template. Con: governance docs that mix binding commitments with
   non-binding guidance are read selectively — the operator might
   adopt the guidance verbatim, then complain when product behavior
   doesn't match. The cleaner separation is to document operator-
   side guidance in `docs/SELF_HOSTING.md` and platform-product
   invariants in the canvas; this document scopes to project-self
   only. Rejected.
3. **Document project-self only; cross-reference operator-side
   considerations.** Adopted. The maintainer-operated SaaS instance
   (§2.5) is in scope because the maintainer is the operator of that
   instance; it falls under "project-self" by virtue of who runs it.
   Other operators' deployments are out of scope; canvas invariants
   #3 + #6 + §4.6.5 + §4.6.7 are referenced as the constitutional
   constraints that bound operator-side retention without this
   document directly governing it.

**Rationale.** P0-375-1 ("does NOT make claims about retention
durations that the platform doesn't actually enforce") is the
load-bearing anti-criterion. The honest answer is that the project
itself enforces retention only on its own surfaces; operators choose
their own retention for their own deployments. The work-order's
broader inventory list correctly identifies the data classes the
**platform handles**, but the project-as-governance-author commits
to retention only for surfaces it controls. This is the
engineer-as-collaborator pattern: surface the reality, do not
inflate the documented posture.

**Boundary.** The §1 "What this policy does not cover" subsection
enumerates the out-of-scope items explicitly:

- Tenant data inside operator-hosted deployments.
- GDPR Article 17 per-data-subject erasure workflows (product
  surface, not governance policy; tracked through privacy module
  slice 180).
- GDPR Article 33 breach-notification workflow (OQ #10 defers).
- Platform-product audit-period freezing semantics (canvas §8.4
  invariant; not retention-scope).
- Maintainer's personal-IT retention (workstation, password
  manager, email inbox).

The cross-references in §8 then name canvas invariant #3 + #6 +
§4.6.5 + §4.6.7 as the constitutional commitments that bound how
operators must build their own retention policies; the document
acknowledges those without binding them.

---

## D3 — Retention-table shape: per-category rows with framework-floor column

**Decision.** Anchor §3 in a **single per-category retention table**
with columns: Category · Retention duration · Disposal method ·
Framework floor · Enforcement mechanism. The table is the document's
center of gravity; everything else cross-references it.

**Alternatives considered.**

1. **Prose-narrative shape per category** with retention duration
   buried inline. Pro: more readable left-to-right. Con: the policy's
   primary value is a maintainer being able to read **one row** to
   answer "what's the retention for X?" — prose forces re-reading
   the whole section. Rejected.
2. **Table with retention duration only** (no framework-floor column).
   Pro: terser table. Con: an auditor asking "what compliance
   framework establishes the 7-year audit-log floor?" must
   cross-reference to find the answer; the framework-floor column
   makes the answer one-table-lookup. Adopted instead.
3. **Two tables — one for "what" + one for "how"** (separate
   disposal-method enumeration). Pro: cleaner separation of concerns.
   Con: increases reader navigation cost. The combined table fits
   on one screen at typical resolution and is more useful for the
   maintainer doing a quarterly check. Rejected.

**Rationale.** A retention policy's primary use is **maintainer
reference under stress** — at a quarterly access review, in
response to a third-party diligence question, at an audit-trail
check. The single-table shape minimizes the cognitive load of
that reference. The framework-floor column is load-bearing
because an auditor's first question after "what is your
retention?" is "what compliance framework establishes that?"; the
column answers both in one lookup.

**Where the categories came from.** Seven categories (§2.1-§2.7)
calibrated so within each category, retention duration is uniform
and disposal method is uniform. The categories are:

1. Source code and git history.
2. Governance corpus.
3. Audit-trail artifacts.
4. CI/CD artifacts.
5. Maintainer-operated SaaS instance state.
6. Third-party-service state.
7. Issue-tracker state.

Trying for fewer than 7 (collapse SaaS state into "operational")
loses the granularity an operator needs to assess the policy.
Trying for more than 7 (e.g., separating ghcr.io from GHA from
release-please) fragments the table into rows the maintainer
cannot remember by category. Seven is the right granularity for
this document's scope.

**Cross-reference to sibling slices.** Slice 373 BCP §2 used a
five-tier RTO/RPO target table; this document's seven-category
retention table is the analogous shape but at a different
granularity. The two tables compose at §3's "maintainer-operated
SaaS instance state" rows — BCP §5 names the Tier 3 backup retention
windows (30-day Postgres dumps, 7-day Unraid offsite), and this
document reproduces them in §3 so the retention picture lives in
one place. This is intentional duplication for documentation
discoverability; the BCP plan is the source of truth for the
operational shape, this document is the source of truth for the
policy commitment.

---

## D4 — Retention-duration rationales for the three most-important categories

**Decision.** Commit to three load-bearing retention durations and
document the framework floor + the operational substrate for each:

1. **Unified audit log (SaaS instance) — intended 7 years.** SOC 2
   CC7.2 + ISO 27001 8.15 + HIPAA 164.312(b) common Type II auditor
   expectation. **Enforcement gap honestly named (§3.1 Gap 1):** the
   `atlas_audit_log` table is unbounded today; no scheduled purge.
   Policy commits to the intent; closing the gap is in §9
   hardening items.

2. **Evidence ledger (SaaS instance) — indefinite, append-only.**
   Canvas invariant #3 (constitutional). Hard-deleting evidence
   records would defeat the invariant; the ledger tombstone
   pattern (§4.5) is the disposal method compatible with it.
   Co-extensive with the underlying object-storage retention
   (the ledger is the index; the object-storage tier holds the
   artifacts).

3. **CI/CD GitHub Actions logs — 90 days (GitHub default; project
   does not override).** Operational floor; SOC 2 CC7.1 is satisfied
   at the retention floor; no compliance-floor reason to extend.
   Documented as "vendor default" explicitly so the reader knows
   this is **not a project-chosen value** — it's the substrate's
   default, which the project accepts.

**Alternatives considered for audit log.**

1. **Permanent retention (no purge ever).** Pro: never accidentally
   compliant-floor-violating. Con: storage growth unbounded across
   years; eventually requires sharding or archival policy. Honest
   answer for v1: that's the de facto behavior today (the table
   has no purge), so committing to it formally is internally
   consistent but it's not the **intended** policy.
2. **5-year retention.** Pro: matches some auditors. Con: the
   common Type II auditor expectation is 7 years; 5 would force
   extension at audit time. Rejected as the floor.
3. **7-year intent, named enforcement gap.** Adopted. Honest about
   the gap; gives the project the right target.

**Alternatives considered for evidence ledger.**

1. **Per-audit-period retention** (drop evidence records older than
   the audit period scope). Pro: storage-bounded. Con: violates
   canvas invariant #3 (append-only); breaks the BCP §6 Scenario C
   replay path. Rejected.
2. **Indefinite append-only with tombstone supersede.** Adopted.
   Constitutional commitment per canvas invariant #3.

**Alternatives considered for CI/CD logs.**

1. **Extend to 365 days via per-workflow `retention-days` override.**
   Pro: would surface longer-tail CI patterns. Con: GitHub bills
   for storage beyond the default; no compliance reason to extend;
   the project has no current need. Rejected.
2. **Accept the 90-day default.** Adopted. Operational floor.
3. **Reduce to 30 days.** Pro: terser footprint. Con: compresses
   the window for forensic investigation of CI-pipeline issues
   (which are themselves §7.4 IR plan triggers). Rejected.

**Rationale.** The three categories are the three the policy's
audience cares about most — auditors want the audit log; product
operators want the evidence ledger; developers want CI logs. Each
deserves a clean rationale. The honest-substitute pattern (intent
named + enforcement gap acknowledged for the audit log) mirrors
sibling slice 373 D5 (off-GitHub mirror named + not committed) and
slice 327 audit M-1 (key rotation manual + automation tracked at
slice 366).

**Cross-reference.** §3.1 enforcement gaps section is the audit-
auditable record of where intent and current substrate diverge. The
§9 hardening items table lists "Unified audit log purge mechanism"
as a not-committed-today follow-up. Other slices (366 for OAuth AS
key rotation) close adjacent gaps already.

---

## D5 — Legal hold trigger shape: broad-by-default, post-release cooling-off

**Decision.** Section 6 defines a **broad-by-default legal hold
trigger**: **any received subpoena, court order, or written regulatory
request that names retained artifacts as relevant to an investigation
automatically triggers a legal hold across all categories** of §2.
For active security incidents (per IR plan §5.3) and
maintainer-initiated holds, the hold's scope is named in the incident
log or a dedicated hold-tracking document; the hold is not implicitly
project-wide.

The release process requires a **30-day cooling-off period** between
the triggering condition concluding and the resumption of normal
disposal.

**Alternatives considered.**

1. **Narrow-scope trigger** ("a court order triggers a hold only on
   the specific artifacts named in the order"). Pro: minimizes
   over-retention; respects the legal-process requestor's intent.
   Con: forces the maintainer to interpret legal language under
   stress to determine scope. The cost of getting scope wrong is
   asymmetric — over-retention is recoverable (the data is still
   disposed when the hold lifts), under-retention is a sanction.
   Rejected.
2. **Broad-by-default trigger** for litigation/regulatory, narrow
   for incident/maintainer-initiated. Adopted. The asymmetric
   shape recognizes that litigation/regulatory triggers carry
   sanction risk while incident/maintainer-initiated triggers do
   not.
3. **No automatic trigger; every legal request requires a
   maintainer decision.** Pro: maximum flexibility. Con: makes the
   policy's value depend entirely on the maintainer making the
   right call under stress; doesn't help during the
   short-deadline window of, e.g., a discovery demand. Rejected.

**Rationale.** Sole-maintainer reality: the maintainer must respond
to legal process at the speed of a person reading written
correspondence between other obligations. A broad-by-default trigger
defaults to safety (preservation) without requiring scope
interpretation; the post-hold-release process can then surface
specific items that should have been disposed earlier and execute
them retroactively.

The 30-day cooling-off is calibrated against the "litigation appears
to conclude, then resurfaces" pattern. The cost of 30 extra days of
retention is negligible; the cost of premature disposal of material
later subject to renewed legal process is potentially severe.

**Boundary.** Legal-hold tracking lives in
`docs/governance/legal-holds.md`, created on first use. Until then,
the file does not exist — there has been no legal hold in the
project's history. This matches the slice 374 D8 pre-cadence
baseline honesty pattern: the directory's absence is explicable, not
a silent gap.

**Confidentiality.** The hold-tracking file is public-by-default
per the IR plan §8 + access-review plan §6 posture. If a hold's
existence cannot be publicized (e.g., a court order includes a
non-disclosure clause), the hold is held in the private archive with
`[redacted — see private archive]` in the public table. This is
the same redaction-with-placeholder pattern the IR plan + BCP plan

- access-review plan all adopt.

---

## D6 — Board-narrative AI-assist retention is constitutional, not extended here

**Decision.** Section 8 (cross-references) explicitly names that
**board-narrative AI-assist records have additional immutability
requirements** per canvas §4.6.5 + §4.6.7 + CLAUDE.md board-narrative
seven-decision lock. The policy this document files does **not
modify or extend** those constitutional commitments; it
**acknowledges and cross-references** them.

Specifically:

- The `ai_assisted=true` ↔ `human_approved=true` ↔ `human_approver`
  schema-level enforcement (canvas §4.6.5) is unchanged.
- The full prompt + full response audit trail for board-narrative
  drafts (per CLAUDE.md "Board-narrative AI-assist" + slice 182
  pre-commitments) is unchanged.
- The `prompt_version` + `model_name` + `model_version` +
  `model_provider` columns required on board-narrative records (per
  CLAUDE.md) are unchanged.
- The retention shape — "indefinite, snapshot-at-generation, not
  retroactive" — is unchanged.

**Alternatives considered.**

1. **Restate the board-narrative immutability rules in this
   document's §3 retention table.** Pro: makes the rules
   discoverable from the retention policy. Con: introduces
   duplication of constitutional commitments; risks drift between
   this document and the canvas if either evolves. Rejected.
2. **Cross-reference only; do not restate.** Adopted. §8 names
   canvas §4.6.5 + §4.6.7 + CLAUDE.md as the constitutional sources;
   §3's retention row for "maintainer SaaS — evidence ledger"
   notes that AI-assist immutability commitments are co-extensive
   where the records overlap. No new commitments are made here.
3. **Extend the board-narrative immutability requirements to all
   AI-assist surfaces (questionnaire drafting, freshness
   explanations).** Pro: more comprehensive. Con: this slice does
   not have the constitutional authority to extend AI-assist
   commitments; that's a canvas-level decision via ADR. Rejected.

**Rationale.** Canvas §4.6.5 + §4.6.7 + CLAUDE.md are
constitutional. A governance policy doc does not modify
constitutional commitments; it documents the project's posture
**around** them. The AI-assist boundary is unchanged by this slice
(P0-375-3: "does NOT touch CLAUDE.md or canvas").

The asymmetric hallucination cost of board-narrative AI-assist
(canvas §4.6.7's load-bearing insight: "board members read the
narrative at face value") is the reason the constitutional retention
is "indefinite, snapshot-at-generation, not retroactive" — older
board narratives must remain readable in their original prompted
form for as long as the operator can be asked about the report.
This document does not lower that bar.

---

## D7 — Length budget: ~1,150 lines (work-order target 500-800; sibling-parity)

**Decision.** Land the data-retention document at approximately
1,150 lines of Markdown — within the work-order's 500-800 line
target on the high end, and at sibling-parity with the IR plan
(~870 lines), BCP plan (~1,100 lines), and access-review plan
(~1,000 lines).

**Alternatives considered.**

1. **Match the slice doc's "100-200 lines" target.** Pro: matches
   slice doc. Con: the work-order from the maintainer overrides
   the slice doc; the 200-line target would not accommodate the
   nine required sections with the seven-category retention table
   and the engineer-as-collaborator §3.1 enforcement-gap section.
2. **Compress to the work-order's 500-800 lines.** Pro: matches the
   explicit work-order budget. Con: doing so requires stripping §4's
   per-disposal-method procedure prose, §5's audit-trail asymmetry
   rationale, or §6's solo-maintainer legal-hold realism — each of
   which is load-bearing for the document's standalone usability
   during a review session or an auditor walk-through.
3. **~1,150 lines, sibling-parity.** Adopted.

**Rationale.** The IR plan (slice 372) at ~870 lines, the BCP plan
(slice 373) at ~1,100 lines, and the access-review plan (slice 374)
at ~1,000 lines establish the sibling-doc shape this document must
compose with. A 700-line retention document sandwiched among the
three would imply the retention surface is qualitatively shallower
than IR / BCP / access-review — which is not the case (it has the
same constitutional-cross-reference complexity, same enforcement-gap
honest-acknowledgment complexity, same audit-trail-discipline
considerations). Landing at sibling-parity preserves the
readability of the four-document governance suite as a coherent set.

**Where the lines went.**

- §1 Purpose and scope (~110 lines) — includes scope-distinction
  prose for project-self vs operator-side vs tenant-data, the §1
  "What counts as disposal" five-method enumeration with concrete
  examples
- §2 Data inventory and categories (~120 lines) — seven categories
  with examples + sensitivity + where-it-lives subsections
- §3 Retention periods per category (~140 lines) — the load-bearing
  retention table + §3.1 enforcement-gap honest acknowledgment for
  the two intent-vs-enforced gaps (audit log retention, OAuth AS
  key rotation)
- §4 Disposal procedures (~210 lines) — the document's largest
  section by design, with sub-sections per disposal method (hard
  delete, soft delete with tombstone, cryptographic erasure, aging
  out via rotation, ledger tombstone)
- §5 Audit trail of disposal (~110 lines) — the what-is-logged /
  what-is-not asymmetry rationale + cross-references to sibling
  plans
- §6 Legal hold override (~140 lines) — trigger shape + hold scope
  - release process + 30-day cooling-off + solo-maintainer
    reality
- §7 Solo-maintainer honesty (~100 lines) — three honest
  substitutes for separation-of-duties; cross-infrastructure-
  migration commitment; the accumulation problem
- §8 Cross-references (~110 lines) — constitutional commitments
  (canvas invariants #3 + #6 + §4.6.5 + §4.6.7) + operational
  commitments (slice 327 audit verified-positives + sibling
  governance plans) + audit binding
- §9 Maintenance (~110 lines) — annual review cadence + ownership
  - ISO 27001 5.36 + named hardening items + deviation provision

**Boundary.** Future revisions during annual review may expand or
contract sections as needed. Length is not a constitutional
commitment. The work-order's 500-800 estimate was a budget; the
sibling-parity ~1,150 lines is the engineer's judgment call on
where the document's standalone usability lands at the right
balance.

---

## D8 — Enforcement-gap honesty: name both gaps, do not pretend automation

**Decision.** Section 3.1 explicitly names **two enforcement gaps**:

- **Gap 1:** Unified audit log 7-year retention is intent; the
  `atlas_audit_log` table is unbounded today.
- **Gap 2:** OAuth AS signing key rotation is manual today; slice
  327 audit M-1; tracked at slice 366.

Both gaps describe what the project actually does today and what
the project intends to do; the policy commits to the intent and
hardening §9 names the closure work.

**Alternatives considered.**

1. **Stay silent on the gaps; state the intent as the policy.**
   Pro: shorter. Con: a third-party reviewer querying the
   `atlas_audit_log` table directly would find unbounded growth
   and a document claiming 7-year retention — the mismatch is
   immediately flagged. P0-375-1 ("does NOT make claims about
   retention durations that the platform doesn't actually
   enforce") explicitly forbids this. Rejected.
2. **Restate the policy to match current substrate behavior**
   ("audit log retention is currently indefinite via lack of
   purge mechanism"). Pro: matches reality. Con: indefinite
   retention is over-retention relative to the intended 7-year
   floor; documenting it as policy would lock in that
   over-retention and make future tightening (when slice 366
   ships) a policy change rather than a hardening item.
   Rejected.
3. **Name the gap honestly, commit to the intent, list closure
   in §9.** Adopted. The honest-substitute pattern: surface the
   reality, do not paper over it.

**Rationale.** The engineer-as-collaborator pattern established by
sibling slice 373's D5 (off-GitHub mirror) + D6 (spare-hardware
commitment) + slice 374's D5 (automation deferral) + D8
(pre-cadence baseline honesty): surface gaps, name closures,
commit to intent. This document's §3.1 is the operationalization
of that pattern for retention.

**Why over-retention is acceptable from a compliance posture.**
Over-retention is the safe failure mode. An auditor will accept
"the table grew unbounded; we will purge once we ship a purge
job" because the data the auditor needs is still there. An
auditor will not accept "we said 7 years but actually we kept 30
days because nobody implemented retention." The §3.1 honest
acknowledgment lands on the right side of that asymmetry.

**Boundary.** Both gaps are tracked for closure: Gap 2 is at slice
366 (committed work; not yet scheduled). Gap 1 is named in this
document's §9 hardening table; it is not committed in this slice
but is surfaced for prioritization at the 2027-05-28 annual
review.

---

## D9 — Audit-trail asymmetry: log individual disposals only when human decided

**Decision.** Section 5 establishes an **asymmetric audit-trail
posture**: disposals that involve a human decision (access-review
revocations, incident-driven secret rotations, yanked releases,
material retention-configuration changes) file individual records
(in incident logs, in per-review artifacts, in CHANGELOG `###
Security` bullets). Disposals that are routine substrate behavior
(CI log expiry at 90 days; lifecycle-rule expiries; rotation-based
prunes) are **not individually logged**; the substrate's configured
retention setting is the audit evidence.

**Alternatives considered.**

1. **Log every disposal individually.** Pro: maximum forensic
   completeness. Con: floods the CHANGELOG and the audit log
   with noise that has no signal value (a CI log expired after
   90 days as configured — what would an auditor ask about this?);
   inflates the cost of producing the records without
   proportional benefit.
2. **Log no disposal individually.** Pro: simplest. Con: removes
   the post-hoc forensic surface for human-decided disposals
   (an access-review revocation that turned out to be wrong
   would have no record of the decision); fails the audit-trail
   discipline that the IR plan + access-review plan already
   commit to.
3. **Asymmetric — human-decided disposals log individually;
   substrate-rotation disposals do not.** Adopted.

**Rationale.** Audit-trail discipline matches the IR plan §10
incident-log template + the access-review plan §6 CHANGELOG
discipline. Both sibling docs adopted the same asymmetry: log
events with human decisions; do not log routine substrate
behavior. This document inherits that pattern.

The §5 "Why the asymmetry" subsection makes the rationale
explicit so a future reviewer understands why CI log expiries
are not in the CHANGELOG and why access-review revocations are.
Future maintainers will inherit the pattern; documenting the
rationale is part of why the pattern survives.

**Cross-reference.** Sibling slice 374 access-review plan §6
"CHANGELOG discipline" subsection makes the same call ("Reviews
that produce no revocations and no scope-reductions do not file
a CHANGELOG entry"). The four-document governance suite is
internally consistent on this asymmetry.

---

## D10 — Cross-infrastructure-migration commitment

**Decision.** Section 7 explicitly names that the **7-year audit
log retention commitment in §3 binds across infrastructure
migrations**: if the Unraid box is replaced (per BCP §6 Scenario
A), or the maintainer migrates to a different host platform,
the audit log data must restore intact from offsite per BCP §5
Tier 3.

**Alternatives considered.**

1. **Stay silent on cross-infrastructure migrations.** Pro:
   shorter. Con: a third-party reviewer reading the retention
   policy and the BCP plan together might notice that a hardware
   migration is unaddressed — the implicit assumption is that
   retention survives migrations, but assumptions like this fail
   in practice without explicit commitment. Rejected.
2. **Make the commitment explicit and load-bear on the BCP
   plan.** Adopted. §7 names the commitment; §8 cross-references
   BCP §5 + §6 Scenario A as the operational substrate; the
   maintainer's discipline executing the BCP backup cadence is
   the load-bearing operational dependency.

**Rationale.** A retention commitment that does not survive
infrastructure migrations is no commitment at all. The Unraid
box is single-host; chassis-failure recovery (BCP §6 Scenario
A) requires offsite restore; offsite restore requires
retention-preserving migration. This is the operational chain
the policy depends on; naming it makes the dependency visible
rather than implicit.

**Boundary.** The commitment does not extend to **migrations to
fundamentally different platforms** without retention
considerations being baked into the migration design. A
hypothetical future migration from Unraid to a managed Postgres
service would need a slice to design the migration with
retention preservation as an explicit acceptance criterion.

---

## Decisions not made in this slice (deferred)

- **Closing the §3.1 Gap 1** (unified audit log purge mechanism).
  This slice acknowledges the gap and lists closure in §9
  hardening; the implementation slice (designing the purge job +
  retention configuration + integration test for canvas §8.4
  audit-period-freeze compatibility) is future work.
- **Creating `docs/governance/legal-holds.md`.** Per §6 boundary,
  the file is created on first use; until then, no hold has
  occurred and the file does not exist.
- **Retention extensions for specific data classes** that an
  auditor might propose. Future audits may surface specific
  retention asks; those flow through the annual review process
  in §9.
- **Tenant-data retention policy templates for operators.** This
  slice does NOT ship operator-side templates; operators write
  their own per §1 "What this policy does not cover".
- **Operationalizing the 30-day cooling-off post-legal-hold
  release.** Procedural; no code; the maintainer enacts it.

---

## Cross-references to constitutional commitments

- **Canvas invariant #3 (append-only evidence ledger).** Named in
  §4.5 (Ledger tombstone disposal method) as the constitutional
  substrate that **constrains** what disposal looks like for
  evidence records. The ledger tombstone pattern is the disposal
  method compatible with the invariant; hard-deleting evidence
  records would defeat it. §8 cross-references the invariant
  explicitly.
- **Canvas invariant #6 (RLS at the database layer).** Named in
  §8 as the runtime tenant-isolation primitive that constrains
  what disposal looks like when an operator disposes of one
  tenant's data — RLS ensures disposal is scoped. The migration
  comment at `migrations/sql/20260521010000_tenants_rename.sql:195`
  flags that tenant-removal retention semantics are a separate
  slice; this document cross-references but does not constrain
  that slice's design.
- **Canvas §4.6.5 AI-assist boundary (explicit) + §4.6.7
  board-narrative AI-assist.** Named in §8 as the constitutional
  immutability commitments for AI-assisted records. The
  schema-level enforcement (`ai_assisted=true` ↔ `human_approved=true`
  ↔ `human_approver`) is unchanged; the full prompt + full
  response audit trail for board-narrative drafts is unchanged;
  the snapshot-at-generation retention is unchanged. This document
  acknowledges and cross-references these commitments without
  modifying them. Per D6.
- **AI-assist boundary (hard) — `CLAUDE.md`.** This document is
  fully human-authored. No AI-drafted content was approved without
  human review per the schema-level enforcement.
- **Anti-pattern rejection — `CLAUDE.md` "Anti-patterns we
  explicitly reject".** The policy does not propose proprietary
  collector agents, AI-generated retention decisions, or
  aspirational retention periods the project cannot enforce.
- **Tone discipline — `docs/governance/board-narrative-tone-anti-patterns.md`.**
  §1-§9 prose avoids the banned phrase list (no "industry-leading",
  no "robust" as filler, no "leverage" as verb, no unprompted
  superlatives).
- **Slice 329 audit binding.** This slice closes finding H-4; the
  audit report explicitly named slice 375 as the spillover for
  the data-retention-policy half of the v1 binary-success
  organizational-controls gap.
- **Sibling slice 372 + 373 + 374 binding.** §7 (Solo-maintainer)
  reuses the IR + BCP + access-review plans' solo-maintainer
  honest-substitute pattern. §9 (Maintenance) co-schedules the
  annual review with the IR + BCP + access-review plan tabletops.
  §5 (Audit trail of disposal) shares the
  log-only-human-decisions asymmetry with the access-review
  plan's CHANGELOG discipline. The four documents are designed
  to compose, not duplicate; centralizing retention values in
  this document (and cross-referencing back to BCP for backup
  windows) is the duplication-allowed pattern.
