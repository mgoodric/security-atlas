# 751 — Board-narrative: exception-status AI-drafted section

**Cluster:** Board / AI-assist
**Estimate:** M (1-1.5d)
**Type:** JUDGMENT (exceptions rollup shape + which exception numbers are board-grade)
**Status:** `not-ready` — blocked on a deterministic exceptions aggregate in `board.Brief`
(or an equivalent RLS-scoped exceptions read the section can ground on).

> Surfaced during slice 501, captured as follow-up per continuous-batch policy.

## Narrative

Slice 501 scaled the board-narrative four-gate machinery to the rollup-grounded
section set (`control_coverage_summary`, `risk_posture_summary`,
`drift_activity_summary`), extracted the reusable numeric-verification library,
and generalized banned-phrase enforcement. The spec proposed an
**exception-status** section as a candidate AI-drafted section; slice 501 did NOT
ship it because **exceptions are not in `board.Brief` today**, so a
rollup-grounded exception section would require a NEW deterministic read path —
net-new source plumbing, out of slice 501's "scale the proven machinery / invent
no number" discipline (slice 501 decisions log D1).

This slice adds the exception-status section once a deterministic exceptions
rollup exists: how many exceptions are open, how many are past their review/expiry
date, the oldest open exception age — every number ground-truth, every supporting
reference a tenant-owned citable id. It inherits the full slice-501 pipeline (four
pre-operator gates, numeric library, banned-phrase check, per-section approval,
`ai_generations` audit row) via a new `SectionDef` — no pipeline change.

## Threat model

Same highest-risk family as slice 440/501 (board narratives reach a non-technical
audience at face value). No NEW guardrail: the section is a new `SectionDef`
consuming the proven machinery. Cross-tenant bleed is covered by the existing
per-section RLS-scoped rollup + citation-ownership gate; the new exceptions read
MUST run under `app.current_tenant`.

## Acceptance criteria

- [ ] **AC-1.** A deterministic exceptions rollup exists (extend `board.Brief`
      with an exceptions summary, or an equivalent RLS-scoped read the section's
      `buildRollup` consumes). Every number the section may state appears in it.
- [ ] **AC-2.** A `SectionExceptionStatus` `SectionDef` (heading + item count +
      rollup-builder + system-prompt + user-prompt) is registered in
      `sectionDefs` + `AIDraftedSections`; the section runs the full four-gate
      pipeline unchanged (P0-501-6 — no new guardrails).
- [ ] **AC-3.** Unit test: a fabricated exception number auto-rejects via the
      reusable numeric library; a correct one passes.
- [ ] **AC-4.** Integration test: the section reaches per-section draft state,
      writes an `ai_generations` row, and a tenant-B draft cannot cite a tenant-A
      exception/control in the section.
- [ ] **AC-5.** Decisions log: which exception numbers are board-grade (open count
      / past-due / oldest age) and the rollup shape.

## Anti-criteria

- Does NOT add or weaken any of the seven guardrails (scales the proven machinery).
- Does NOT auto-approve; does NOT publish without per-section one-click approval.
- Does NOT invent an exception number not in the deterministic rollup.

## Dependencies

- **#501 (this arc)** — the section abstraction (`SectionDef`), the reusable
  numeric library, the per-section approval + assembly read. **Hard dependency.**
- A deterministic exceptions aggregate in `board.Brief` (or an exceptions read
  the section grounds on) — the blocking prerequisite.

## Notes

Source: slice 501 decisions log "Revisit once in use" #1. The companion follow-ons
named in the slice-501 spec (scheduled board-report cadence/distribution;
narrative-level diff between successive packs) are tracked in that spec's
follow-on notes and are NOT this slice.
