# 336 — UX flow validation via voltagent-qa-sec:ui-ux-tester

**Cluster:** Frontend
**Estimate:** 2d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Runs `voltagent-qa-sec:ui-ux-tester` against the platform's
documented user flows, validating that each works end-to-end and
matches the operator's mental model. This is the **product UX
view** — companion to slice 331's WCAG conformance audit. Where 331
asks "is the UI usable?", this slice asks "does the UI tell the
right story?"

The v1 binary success test is fundamentally about user experience:
"does the user run their next SOC 2 out of security-atlas?" A
platform that's technically functional but UX-confusing fails the
test. This audit catches the failure modes before the user
discovers them.

**Audit surface.** End-to-end flow validation across:

- **Login + tenant switch.** Per slice 191's auth-substrate-v2 +
  slice 141's tenant-switcher. The operator types email/password
  (or OIDC), lands on dashboard for their default tenant, can
  switch to another tenant without re-auth. UX assertion: the
  tenant context is always visible; switching is one click.
- **Dashboard.** First-page-after-login. Hero metrics + control
  health + risk hierarchy summary + recent activity. UX assertion:
  the operator can identify the program's current state in <30s
  without clicking.
- **Control detail.** Drill from dashboard → control. Slice 010's
  control kit + slice 064's backend endpoints + slice 041's UI.
  UX assertion: control's state + evidence + scope + framework
  satisfactions are all visible without scrolling past the fold.
- **Audit workspace.** Slice 028 (audit-period freezing) + slice
  029 (audit hub comments). UX assertion: the auditor's mental
  model (sample → evidence → test → conclude) is reflected in the
  surface.
- **Risk hierarchy.** Slice 056's hierarchical risk dashboard +
  slice 019's risk CRUD. UX assertion: residual risk is the
  default view; inherent + controls + weighting are discoverable
  but not foregrounded.
- **Evidence inspection.** Drill from control → linked evidence →
  raw record. UX assertion: provenance is always visible.
- **Board pack preview.** Slice 031 + 032 (monthly + quarterly).
  UX assertion: the operator can preview the actual board-ready
  output and edit per-section before send.
- **Admin surfaces.** Slice 062 (admin BFF) + slice 143 (create
  tenant) + slice 142 (super-admin management). UX assertion: the
  operator role is always visible; super-admin surfaces are
  visually distinct from per-tenant admin.
- **Onboarding walkthrough.** Slice 070's walkthroughs. UX
  assertion: the new operator finds the first walkthrough without
  knowing it exists.

**Why now:** the surfaces have accumulated independently across
~250 slices. End-to-end flow validation catches the
inter-surface-coordination bugs that per-surface tests miss.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 10/12.

**Disposition:** read-only UX audit + follow-up-slice fan-out.

## Threat model

UX-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only — UX validation observes; doesn't
  modify product behavior. AC enforces.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/336-ux-flow-validation-decisions.md`.
- **I (Information disclosure):** Demo seed only. AC enforces.
- **D (Denial of service):** CLEAN.
- **E (Elevation of privilege):** Dev-level access; the audit
  exercises both regular-admin and super-admin surfaces but only
  with the demo-tenant identities.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:ui-ux-tester` agent runs
      against the nine documented flows.
- [ ] **AC-2.** Findings recorded in
      `docs/audit-log/336-ux-flow-validation-decisions.md` per
      flow: UX assertion · observed behavior · pass/fail/partial ·
      deviation description.
- [ ] **AC-3.** **Flow-breaking findings** (the user cannot
      complete the flow without engineer intervention OR a
      critical step is hidden) fan out as individual
      `/idea-to-slice` follow-up slices.
- [ ] **AC-4.** **Polish findings** (the flow works but the UX
      misleads, frictions, or surprises) bundle into a "UX polish
      round 1" slice OR per-surface individual slices.
- [ ] **AC-5.** Cross-references slice 331 (a11y audit) — same
      flow may have a11y issues AND UX issues; dedupe at
      follow-up-filing time.
- [ ] **AC-6.** Cross-references slice 178 (UI honesty audit
      harness) — UX findings that the harness could catch get
      flagged as "candidate harness extension".
- [ ] **AC-7.** Demo seed only (slice 205 dataset). AC-8 enforces.
- [ ] **AC-8.** Each flow walked through with each of: admin role,
      auditor role (per slice 025), super-admin role (per slice
      142). Per-role pass/fail tracked.
- [ ] **AC-9.** No code modified. Diff = doc files only.
- [ ] **AC-10.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **Manual evidence is first-class (canvas §4.5).** The UX must
  reflect the manual-evidence path as a peer to automated
  collection.
- **AI-assist boundary (CLAUDE.md).** The UX must surface
  human-approval gates clearly on every AI-assisted surface.
- **Survive third-party security review (canvas §6).** UX
  defects in auth + tenant-switch + audit surfaces are
  security-adjacent (operator confusion → security mistake).

## Canvas references

- `Plans/canvas/01-vision.md` §3 — primary user persona
- `Plans/canvas/08-audit-workflow.md` — auditor flow
- `Plans/canvas/07-metrics.md` — board reporting flow

## Dependencies

- Slices that ship the flows under audit — all merged.
- **#205** (demo seed) — `merged`. Provides the data the flows
  exercise.

## Anti-criteria (P0 — block merge)

- **P0-336-1.** Does NOT exercise production tenant data — demo
  seed only.
- **P0-336-2.** Does NOT run UX validation against atlas-edge
  with real-customer data.
- **P0-336-3.** Does NOT bundle flow-breaking findings into one
  slice. Tracer-bullet per flow break.
- **P0-336-4.** Does NOT auto-merge.
- **P0-336-5.** Does NOT modify code.
- **P0-336-6.** Does NOT include screenshots with PII or
  customer-attributable strings in the decisions log.
- **P0-336-7.** Does NOT touch CLAUDE.md, canvas, mockups.

## Skill mix

- `voltagent-qa-sec:ui-ux-tester` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Playwright + browser DevTools for flow recording

## Notes for the implementing agent

**Per-flow walkthrough template (use as the audit checklist):**

```
### Flow: <name>

**Entry point:** <URL or trigger>
**Goal:** <what the user wants to accomplish>
**Pass criterion:** <observable success state>

**Steps observed:**
1. <action> → <observed result>
2. ...

**Pass/fail/partial:** <result> per role (admin / auditor / super-admin)
**Deviations:** <list of unexpected behaviors>
**UX friction points:** <list of confusion-causing surfaces>
```

**Three roles to walk through (load-bearing for AC-8):**

- **Admin** — regular tenant admin; the v1 primary user.
- **Auditor** — slice 025's scoped-access role; visits only
  audit-relevant surfaces.
- **Super-admin** — slice 142's cross-tenant role; visits the
  cross-tenant admin surfaces.

**Cross-reference protocol.** A flow finding that's also a WCAG
issue gets noted "candidate dedupe with slice 331 finding #NN".
A flow finding that the UI honesty harness could catch gets
noted "candidate harness extension via slice 178".

**Audit log filename:**
`docs/audit-log/336-ux-flow-validation-decisions.md`
