# 446 — Disclosure / breach-notification workflow decision spike + ADR (no code)

**Cluster:** Privacy
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (decision-only)
**Status:** `ready`

> **OPEN QUESTION SURFACED TO MAINTAINER (OQ #10).** This slice touches
> `Plans/canvas/11-open-questions.md` #10 ("Disclosure / breach-notification
> workflow scope — HIPAA breach rule and GDPR Art. 33 … v1 punts; phase 3 lands
> them"). Per the `/idea-to-slice` open-question rule, this is flagged before
> proceeding: this slice is a **decision spike that produces the design**, not an
> implementation, and it **does not auto-merge** — the maintainer approves the
> recommended shape before any breach-workflow code is filed. OQ #7's privacy
> resolution already sketched a preliminary shape ("incident lives in security
> module; 72-hour notification workflow lives in privacy module; structured
> handoff at the 'breach confirmed' state transition") — this spike formalizes
> that sketch into an ADR.

## Narrative

OQ #10 defers the HIPAA-breach / GDPR Art. 33 (72-hour) notification workflow,
and OQ #7's privacy resolution committed the **sibling-module** architecture
(privacy primitives in their own `privacy.*` Postgres schema, sharing auth /
tenancy / RLS / audit-log) and sketched the breach handoff: the incident lives
in the **security** module; the 72-hour notification workflow lives in the
**privacy** module; the handoff happens at the "breach confirmed" state
transition. The privacy **foundation** landed at slice 180 (audit-log
`subject_module` column, feature-flag module-toggling, sibling-discipline doc).

But the **security → privacy "breach-confirmed → 72h notification" handoff is
unspecified**: what is the state-machine, what entity owns the 72-hour clock,
how is the notification-target register modeled, and how does the cross-module
seam (OQ #7's B3 rule: privacy may reference `evidence.id` / `policy.id` but NOT
`controls.id` directly) constrain the design. This is exactly the kind of
load-bearing decision that warrants a **decision spike like slice 400 was for
cosign** — settle the shape in an ADR before any implementing engineer commits
to a state-machine unilaterally.

**Scope discipline.** **Decision-only: no production code ships.** The
deliverable is an ADR plus a thin schema **sketch** (not a migration). It models
the security→privacy breach-confirmed handoff, the 72-hour-clock entity, and the
notification-target register; it produces a confidence-rated recommendation and a
follow-on implementation-slice breakdown. It does **not** implement the workflow,
does **not** add a migration, and does **not** decide the broader privacy-module
ship timing (that stays OQ #7's "privacy v0 ships at v2+ when a prospect surfaces
demand"). **Follow-on slices (filed only after maintainer approves the ADR):**
the breach-state-machine implementation; the 72h-clock entity + migration; the
notification-target register; the security↔privacy handoff wiring.

## Threat model (STRIDE) — decision-spike scope

This slice ships **no runtime code**, so its threat surface is the **design it
recommends**, not a running system. The STRIDE pass here is a forward-looking
checklist the ADR must address so the _future_ implementation inherits the right
constraints — substantive because breach data is among the most sensitive in the
platform (confirmed-breach records + affected-data-subject registers).

**S — Spoofing.** The ADR must specify that breach-confirmation + notification-
dispatch are high-privilege actions (who can confirm a breach? who can mark a
notification sent?) — these cannot be ordinary-user actions.
**ADR must address:** the role boundary for breach-confirm + notification actions.

**T — Tampering.** The 72-hour clock is legally load-bearing (GDPR Art. 33
deadline); its start time (breach-confirmed timestamp) must be immutable once set.
**ADR must address:** clock-start immutability + an append-only state-transition
record (so a missed deadline cannot be retroactively hidden).

**R — Repudiation.** Breach-notification is a compliance obligation; every state
transition (detected → confirmed → notification-drafted → notification-sent) must
be auditable, reusing slice 180's `subject_module` audit-log column.
**ADR must address:** the audit-trail design across the security↔privacy seam.

**I — Information disclosure.** Confirmed-breach records + affected-subject
registers are extremely sensitive + tenant-scoped; the cross-module reference
seam (OQ #7 B3) must not leak.
**ADR must address:** RLS across the `privacy.*` schema for breach entities; the
B3 reference constraint (no direct `controls.id` reference); minimum-disclosure
in any notification artifact.

**D — Denial of service.** Forward-looking only — the ADR should note bounding
for the affected-subject register (a breach could touch millions of subjects).
**ADR must address:** a note on register-scale bounding for the implementation.

**E — Elevation of privilege.** Breach-confirm is the gate that starts a legal
clock; it must be a deliberate, audited, high-privilege transition — never
automatic.
**ADR must address:** breach-confirmation is a human-initiated, audited action;
no automatic confirmation.

## Acceptance criteria

**ADR (the deliverable)**

- [ ] **AC-1.** An ADR (`docs/adr/NNNN-breach-notification-workflow.md`, next ADR
      number) is authored covering: the security→privacy breach-confirmed
      handoff state-machine; the 72-hour-clock entity; the notification-target
      register; and how the cross-module seam (OQ #7 B3 reference constraint) is
      honored.
- [ ] **AC-2.** The ADR includes a **thin schema sketch** (entity shapes +
      relationships, NOT a migration) for the breach-state record, the clock
      entity, and the target register.
- [ ] **AC-3.** The ADR addresses each STRIDE forward-looking item above
      (clock-start immutability, append-only transitions, RLS across `privacy.*`,
      breach-confirm role boundary, no-automatic-confirmation).
- [ ] **AC-4.** The ADR includes a **confidence-rated recommendation** (ADOPT /
      ADOPT-DEFERRED / REVISE) + the recommended phase (per OQ #10, this is
      phase-3 / privacy-v0 work — the ADR confirms or revises that timing).
- [ ] **AC-5.** The ADR records the **follow-on implementation-slice breakdown**
      (the discrete ready/not-ready slices that would implement the workflow,
      gated on maintainer approval of this ADR).

**Process**

- [ ] **AC-6.** **No production code changed** — ADR + the schema sketch (as ADR
      content) only; no migration, no Go package, no endpoint.
- [ ] **AC-7.** A changelog entry noting the ADR (decision artifact).

## Constitutional invariants honored

- **OQ #7 privacy resolution.** Sibling-module architecture; privacy entities in
  `privacy.*`; B3 reference constraint (privacy MAY reference `evidence.id` /
  `policy.id`, MUST NOT reference `controls.id` directly). The ADR honors this.
- **#6 — Tenant isolation via RLS.** The ADR specifies RLS across the new
  `privacy.*` breach entities.
- **Slice 180 foundation.** Reuses the audit-log `subject_module` column +
  feature-flag module-toggling.
- **AI-assist boundary — N/A at design time**, but the ADR notes any future
  notification-drafting AI assist would fall under the boundary (cited, human-
  approved).

## Canvas references

- `Plans/canvas/11-open-questions.md` #10 (disclosure/breach scope) + #7 (privacy
  sibling-module resolution incl. the preliminary breach-handoff sketch).
- `Plans/canvas/06-risk.md` — incident/risk linkage (the security-side incident).

## Dependencies

- **#180** (privacy foundation — `subject_module` audit column, feature-flag
  module-toggling, sibling-discipline doc) — `merged`. The foundation this ADR
  builds the handoff on.
- **#400** (cosign decision spike) — `merged`; the **shape template** for this
  decision-only ADR slice (not a code dependency).

## Anti-criteria (P0 — block merge)

- **P0-446-1.** Ships **NO production code** — if the spike tempts a "quick
  schema migration" or a state-machine prototype, that belongs in a follow-on
  implementation slice filed after maintainer approval, not here (slice-400
  precedent).
- **P0-446-2.** Does **NOT auto-merge** — the recommendation is for maintainer
  sign-off; this is the human decision point for OQ #10 / the breach workflow.
- **P0-446-3.** Does NOT decide the broader privacy-module ship timing — that
  stays OQ #7's "privacy v0 at v2+ on prospect demand."
- **P0-446-4.** Does NOT introduce a direct `privacy → controls.id` reference in
  the sketch (OQ #7 B3 constraint).
- **P0-446-5.** Does NOT design automatic breach-confirmation — confirmation is
  a human-initiated, audited action (threat-model E).

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (schema-sketch shape, NOT a migration) ·
`Thinking` (Red Team the proposed state-machine for the STRIDE forward-items) ·
`simplify` (ADR clarity) · `changelog-generator`.

## Notes for the implementing agent

- **This is the maintainer's "spike/ADR first, then decide" gate for the breach
  workflow** — mirror slice 400's shape exactly: value + tradeoffs + a
  confidence-rated recommendation + a follow-on slice breakdown. The
  implementation slices are deliberately NOT filed until this ADR lands and the
  maintainer approves the direction.
- **JUDGMENT calls you own:** the proposed state-machine shape, the clock-entity
  design, and the target-register model — make the call, record it in the ADR,
  and let the maintainer iterate. Do NOT return to the caller for these; only a
  genuine constitutional conflict warrants escalation.
- The OQ #10 surfacing at the top of this file is the open-question disclosure;
  the maintainer reads it before approving.
- Detection-tier: `none` (decision-only; no bug surface).
