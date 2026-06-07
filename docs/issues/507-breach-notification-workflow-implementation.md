# 507 — Breach-notification workflow implementation (security → privacy 72-hour handoff)

**Cluster:** Privacy
**Estimate:** L (4-5d)
**Type:** JUDGMENT (state-machine + 72h-clock semantics)
**Status:** `not-ready`

> **GATED downstream of the slice-446 decision spike.** Slice 446 is the
> **decision-gate** for OQ #10 (disclosure / breach-notification workflow,
> HIPAA breach rule + GDPR Art. 33 72-hour notification). Slice 446 is
> decision-only (no code) and **does not auto-merge** — the maintainer approves
> the recommended ADR shape before any breach-workflow code is filed. **This is
> the first downstream implementation slice 446 explicitly names** ("Follow-on
> slices, filed only after maintainer approves the ADR: the breach-state-machine
> implementation; the 72h-clock entity + migration; the notification-target
> register; the security<->privacy handoff wiring"). It cannot move to `ready`
> until slice 446's ADR is ratified AND privacy-v0 is greenlit (the privacy half
> of the handoff lives in the deferred privacy sibling module).

## Narrative

**WHY.** GDPR Art. 33 requires notification of a personal-data breach to the
supervisory authority **within 72 hours** of becoming aware of it; the HIPAA
breach-notification rule imposes parallel obligations. OQ #7's privacy resolution
sketched the architecture — the incident lives in the **security** module; the
72-hour notification workflow lives in the **privacy** module; the handoff happens
at the "breach confirmed" state transition, subject to OQ #7's B3 cross-module
rule (privacy may reference `evidence.id` / `policy.id` but NOT `controls.id`
directly). Slice 446 formalizes that sketch into a ratified ADR. **This slice
implements the ADR.**

**WHAT this slice ships (once ungated — exact shape inherits slice 446's ADR).**

1. **Breach-state-machine.** State transitions
   `detected -> confirmed -> notification-drafted -> notification-sent ->
closed`, with `confirmed` as the security->privacy handoff point. Each
   transition is append-only and role-gated per the ADR.
2. **72-hour-clock entity** (`privacy.*` namespace). The clock starts at the
   `confirmed` timestamp; the start time is **immutable once set** (the legal
   deadline anchor). A countdown surface and overdue alarm are derived at read
   time (no cron required, matching the exception-expiry / policy-ack pattern).
3. **Notification-target register.** Who must be notified per applicable regime
   (supervisory authority, affected data subjects, business associates), modeled
   in the privacy module, referencing evidence/policy ids only (B3 rule).
4. **Security<->privacy handoff wiring.** At `confirmed`, the security-side
   incident hands a structured payload to the privacy-side workflow across the
   sibling seam — referencing `evidence.id` for the breach evidence, never
   reaching into `controls.id` directly.
5. **Append-only state-transition log.** Every transition writes a
   `subject_module='privacy'` audit-log row so a missed 72-hour deadline cannot be
   retroactively hidden (slice 446's tampering mitigation, now enforced in code).

**SCOPE DISCIPLINE — what's deliberately out.**

- **The ADR itself** — slice 446. This slice consumes it; if 446's ratified shape
  differs from this spec's sketch, this spec is updated at the grill-with-docs
  gate before coding.
- **The general incident-response process docs** — slice 372 (IR plan, merged)
  owns the human process; this slice implements the _notification_ sub-workflow
  the IR plan triggers, not the whole IR lifecycle.
- **Email delivery of notifications** — composes with slice 445 (email/SMTP
  delivery substrate) as the sink; this slice produces the notification records,
  445's substrate delivers them. It does not re-implement delivery.
- **Auto-drafting notification prose via LLM.** Notification content is a
  legally-binding artifact; the AI-assist boundary forbids publishing an
  audit-binding artifact without one-click human approval, and breach notices are
  not on the sanctioned AI-assist surface list. The workflow is template + human.

## Threat model (STRIDE)

Breach data is among the **most sensitive in the platform** (confirmed-breach
records + affected-data-subject registers), and the 72-hour clock is
**legally load-bearing**. This is a high-stakes surface.

**S — Spoofing.** Breach-confirmation and notification-sent are high-privilege
actions; an attacker confirming a false breach (or marking a real one "sent"
without sending) could cause legal harm. **Mitigation:** breach-confirm +
notification-dispatch are gated to an explicit high-privilege role per the ADR
(not ordinary-user actions); the acting identity is recorded on every transition.

**T — Tampering (PRIMARY).** The 72-hour clock's start time (confirmed timestamp)
must be immutable once set — a mutable start time would let a missed deadline be
back-dated to appear compliant. **Mitigation:** the clock-start column is set once
and never updated (enforced in SQL: no UPDATE path to the start column); the
append-only transition log records the true confirmed timestamp independently.

**R — Repudiation.** A missed deadline must be undeniable. **Mitigation:** every
state transition writes an append-only `subject_module='privacy'` audit-log row
(four-policy RLS); the overdue state is derived from the immutable clock-start, so
it cannot be suppressed by editing a status field.

**I — Information disclosure.** Breach + affected-subject registers are the
crown-jewel sensitivity tier. **Mitigation:** RLS tenant-scopes all breach
tables; the security<->privacy handoff carries only the minimum payload across the
seam; access to the breach workflow is confined to the high-privilege role.

**D — Denial of service.** A breach event is operator-driven, not high-volume.
N/A beyond standard limits; the read-time clock derivation avoids a cron that
could be starved.

**E — Elevation of privilege.** The B3 cross-module rule is a containment
boundary: a compromise of the privacy module must not yield `controls.id` write
access. **Mitigation:** the handoff references `evidence.id` / `policy.id` only;
no `controls.id` reference crosses the seam (CI lint per slice 180's B4 rule once
`internal/api/privacy/` exists).

## Acceptance criteria

- [ ] **AC-1.** The breach-state-machine transitions match slice 446's ratified
      ADR; `confirmed` is the security->privacy handoff point.
- [ ] **AC-2.** The 72-hour-clock start time is immutable once set (integration
      test asserts no UPDATE path mutates it).
- [ ] **AC-3.** The overdue state is derived at read time from the immutable
      clock-start (no cron); integration test asserts overdue surfaces correctly.
- [ ] **AC-4.** Every transition writes a `subject_module='privacy'` append-only
      audit-log row.
- [ ] **AC-5.** Breach-confirm + notification-dispatch are gated to the ADR's
      high-privilege role (403 below it).
- [ ] **AC-6.** The handoff references `evidence.id` / `policy.id` only — no
      `controls.id` reference crosses the sibling seam (B3 rule).
- [ ] **AC-7.** Notification records integrate with slice 445's delivery
      substrate as the sink (not a re-implemented delivery path).
- [ ] **AC-8.** This spec is reconciled against slice 446's ratified ADR at the
      grill gate before coding; divergences update the spec, not papered over.

## Anti-criteria (P0 — block merge)

- **P0-507-1.** Does NOT permit mutating the 72-hour clock start time.
- **P0-507-2.** Does NOT auto-draft notification prose via LLM (AI-assist
  boundary: no AI-authored legally-binding artifact).
- **P0-507-3.** Does NOT reference `controls.id` across the privacy seam (B3).
- **P0-507-4.** Does NOT begin before slice 446's ADR is ratified AND privacy-v0
  is greenlit.

## Dependencies

- **#446** (breach-notification decision spike) — `ready`, decision-gate. Hard
  gate: its ADR must be ratified before this slice starts.
- **#180** (privacy-module foundation) — `merged`. Provides `subject_module` +
  B3/B4 sibling rules.
- **#372** (IR plan) — `merged`. The human process this notification sub-workflow
  serves.
- **#445** (email/SMTP delivery substrate) — `ready`. The notification sink.
- **Privacy-v0 greenlight** — pending real-prospect demand (OQ #7). Hard gate
  (the privacy half of the handoff).

## Canvas references

- `Plans/canvas/11-open-questions.md` #10 (breach-notification workflow) + #7
  (privacy sibling-module + B3 cross-module rule)
- `docs/issues/446-breach-notification-workflow-spike.md` (the gating ADR spike)

## Constitutional invariants honored

- **#6** RLS tenant isolation — all breach tables tenant-scoped.
- **AI-assist boundary** — notification prose is template + human-approved.
- **Sibling-module B3 rule** — privacy references `evidence.id` / `policy.id`,
  never `controls.id`.
