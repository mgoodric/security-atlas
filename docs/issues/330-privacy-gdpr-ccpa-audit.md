# 330 — Privacy audit (GDPR + CCPA) via voltagent-qa-sec:gdpr-ccpa-compliance

**Cluster:** Compliance
**Estimate:** 1.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Runs `voltagent-qa-sec:gdpr-ccpa-compliance` against security-atlas
focused specifically on data privacy law (GDPR + CCPA + CPRA). This is
narrower than slice 329's GDPR coverage — slice 329 asks "does the
platform pass an audit?"; this slice asks "does the platform's design
respect data-subject rights, lawful-basis discipline, and
controller-vs-processor distinction?"

Privacy is the corner of compliance where the platform's design
implications are most concrete: every byte of evidence (control IDs,
risk owners, vendor contacts, audit trails) is potentially personal
data, and the platform serves a market (EU + California self-host
operators) where the data subject's right to deletion conflicts with
the platform's append-only evidence ledger invariant.

**Audit surface.** Privacy-specific pass across:

- **Data subject access requests (DSAR).** GDPR Art. 15 / CCPA §1798.110.
  Can a user export all their personal data held by the platform? Across
  evidence records, audit logs, board narratives, OPA decision logs?
- **Right to erasure / right to deletion.** GDPR Art. 17 / CCPA §1798.105.
  How does the platform handle the conflict with the append-only
  evidence ledger (invariant #3) and audit-period freezing (canvas §8.4)?
  Tombstone / pseudonymize / refuse-with-explanation are all valid
  designs — but ONE of them must be the documented design.
- **Consent management.** GDPR Art. 7. Does the platform track consent
  for any processing that requires it (analytics, telemetry — currently
  none per GOVERNANCE.md, but verify)?
- **Records of processing.** GDPR Art. 30. Is there a Records of
  Processing Activities (RoPA) document for the platform's own
  processing of operator + customer data?
- **Lawful basis.** GDPR Art. 6. Per processing purpose, which lawful
  basis applies? Most likely contract (for paid SaaS) or legitimate
  interest (for OSS self-host operator data). Document explicitly.
- **Controller / processor distinction.** GDPR Art. 4(7-8) / Art. 28.
  For the self-hosted deployment model, the operator is the controller
  and security-atlas the open-source product is neither (no data
  transfer). For the hosted offering (open question #03 in
  `Plans/canvas/11-open-questions.md`), the hosted operator becomes
  the processor. Audit needs to keep these straight.
- **Cross-border transfer mechanics.** GDPR Chapter V. If atlas-edge or
  any future hosted offering moves data out of the EU, the transfer
  mechanism (SCC, BCR, adequacy decision) must be documented.
- **Privacy by design.** GDPR Art. 25. Cross-references slice 180's
  privacy-module foundation — does the implementation match the design
  intent?
- **Breach notification.** GDPR Art. 33-34 / CCPA equivalent. Does the
  platform have a documented breach-notification workflow shape (open
  question #11 in `Plans/canvas/11-open-questions.md` — explicitly
  deferred)?

**Why now:** the privacy module foundation (slice 180) was scoped as
"foundation"; the actual application of GDPR / CCPA to the platform's
design has not been systematically audited. With ~250 slices merged,
the design surface is wide enough that a privacy-first review will
surface concrete gaps.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 4/12.

**Disposition:** read-only privacy audit + follow-up-slice fan-out.

## Threat model

Privacy-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/330-privacy-gdpr-ccpa-audit-decisions.md`.
- **I (Information disclosure):** Privacy findings may name specific
  data categories the platform processes (evidence content, audit-log
  detail, risk-owner contacts). These categories are part of the
  product surface and are not themselves confidential. CLEAN with
  standard "no production data" caveat.
- **D (Denial of service):** CLEAN.
- **E (Elevation of privilege):** Dev-level access only.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:gdpr-ccpa-compliance` agent
      runs against the nine privacy surfaces in the narrative.
- [ ] **AC-2.** Decisions log at
      `docs/audit-log/330-privacy-gdpr-ccpa-audit-decisions.md`
      records per-surface findings: present / partial / absent ·
      evidence pointer · gap.
- [ ] **AC-3.** **Load-bearing finding: right-to-erasure design.**
      The decisions log MUST document the platform's current handling
      of erasure-vs-append-only and propose a default design
      (tombstone / pseudonymize / refuse-with-explanation). If the
      design is "we haven't decided yet", file a follow-up slice for
      the decision.
- [ ] **AC-4.** **Load-bearing finding: RoPA.** If a Records of
      Processing Activities document does not exist, file a follow-up
      slice to create one.
- [ ] **AC-5.** **Load-bearing finding: DSAR export workflow.** If
      there's no documented way to export all personal data held about
      a user, file a follow-up slice for the workflow.
- [ ] **AC-6.** Cross-references with slice 329 noted (GDPR overlap;
      329 is the meta-audit perspective, 330 is the design-implication
      perspective; same finding can show up in both — dedupe at
      follow-up-filing time).
- [ ] **AC-7.** No code modified. Diff = doc files only.
- [ ] **AC-8.** Open question #11 (breach-notification workflow shape)
      is NOT pre-decided by this slice. If the audit surfaces design
      pressure on it, document the pressure and the deferred decision
      stays deferred.
- [ ] **AC-9.** `pre-commit run --files` passes at PR-time.

## Constitutional invariants honored

- **Append-only evidence ledger (invariant #3).** Erasure-design must
  not corrupt the ledger's audit-period integrity.
- **Audit-period freezing (canvas §8.4).** Erasure during a frozen
  period must preserve the period's sample population.
- **Manual evidence is first-class (canvas §4.5).** RoPA and DSAR
  workflow are themselves manual-evidence-style artifacts.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 (append-only) +
  §4.5 (manual evidence) — erasure design space
- `Plans/canvas/08-audit-workflow.md` §8.4 — audit-period freezing
- `Plans/canvas/11-open-questions.md` items #03, #09, #11 — hosted
  offering, privacy module shape, breach-notification

## Dependencies

- **#180** (privacy module foundation) — `merged`. Provides the
  surface anchor.

## Anti-criteria (P0 — block merge)

- **P0-330-1.** Does NOT pre-decide open question #03 (hosted offering
  shape) — if it surfaces, flag it without resolving.
- **P0-330-2.** Does NOT pre-decide open question #09 (privacy-module
  sibling-vs-first-class) — same discipline.
- **P0-330-3.** Does NOT pre-decide open question #11 (breach-notification
  workflow shape) — AC-8 enforces.
- **P0-330-4.** Does NOT bundle major findings (DSAR / erasure / RoPA)
  into one follow-up slice. Each is its own tracer-bullet.
- **P0-330-5.** Does NOT modify code.
- **P0-330-6.** Does NOT auto-merge.
- **P0-330-7.** Does NOT touch CLAUDE.md, canvas, mockups.

## Skill mix

- `voltagent-qa-sec:gdpr-ccpa-compliance` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Standard read/grep — surface enumeration

## Notes for the implementing agent

**Right-to-erasure: the design-space worth surveying explicitly.**
Three credible designs:

- **Tombstone.** Mark the record `erased_at = NOW()`, redact PII
  fields, retain integrity hash. Audit-period freezing still works
  (the period sees the tombstone). Discoverable in the SDK as
  `Subject.erased=true`.
- **Pseudonymize.** Replace identifier fields with stable hash;
  retain operational content. Useful when the _what_ matters but the
  _who_ doesn't. Audit-period freezing sees pseudonymized data.
- **Refuse-with-explanation.** GDPR Art. 17(3) has carveouts for
  compliance with legal obligations + establishment / exercise /
  defense of legal claims. For evidence linked to an active audit
  period, refusal is lawful and audit-defensible. The platform
  surfaces the carveout to the operator + records the refusal.

The agent should NOT pick one — surfacing the design space + the
trade-offs is the deliverable. The actual decision is a follow-up
slice's content.

**RoPA shape suggestion.** Six columns: processing purpose · data
categories · data subjects · recipients · retention period · transfer
mechanism (if any). Per-purpose row. The agent should document
whether the current codebase + GOVERNANCE.md surface enough to fill
the table OR whether the operator needs to fill it themselves.

**Cross-reference protocol.** Findings that overlap slice 329's GDPR
coverage: note "candidate dedupe with slice 329 finding #NN". The
maintainer decides ownership.

**Audit log filename:**
`docs/audit-log/330-privacy-gdpr-ccpa-audit-decisions.md`
