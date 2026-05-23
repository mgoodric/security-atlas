# 215 — Audits page title status tally missing ("1 in progress · 4 frozen · 1 closed")

**Cluster:** frontend
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (audits page), captured as
follow-up per continuous-batch policy.

The mockup at `Plans/mockups/audits.html` renders a status tally
inline with the H1 (`Plans/mockups/audits.html` lines 109-111):

```
<h1>Audit periods</h1>
<span class="text-sm text-slate-500">
  1 in progress · 4 frozen · 1 closed
</span>
```

This is a one-glance summary the operator uses to confirm "this is
the right list" before scanning rows. It's also the surface the
board narrative AI-assist will eventually read from the page DOM
when summarizing audit state.

The live page renders the H1 plus a subtitle:

> "Period-level index — open a period for the per-control walk-through"

…but no status tally. With zero periods in the test tenant today,
the absence is invisible. With many periods (the realistic state of
a year-old install), the absence is a real navigational loss.

## Threat model

**Verdict.** **no-mitigations-needed.** Pure derived UI from the
already-fetched periods list. No new data path; no new endpoint.

## Acceptance criteria

- **AC-1.** When `periods.length > 0`, the H1 row renders an inline
  status tally formatted: `<N> in_progress · <N> frozen · <N>
closed · <N> open`. Statuses with count 0 are omitted from the
  rendered tally (so "all frozen" renders just `6 frozen`).
- **AC-2.** When `periods.length === 0`, no tally renders (consistent
  with the existing empty-state pattern).
- **AC-3.** Tally is `aria-label="audit period status tally"` for
  screen-reader clarity.
- **AC-4.** Vitest test confirms the formatter:
  - All four statuses → all rendered
  - Single status → only that status rendered (`6 frozen`)
  - Empty → empty string
- **AC-5.** Playwright e2e spec asserts the tally appears with
  seeded fixture periods in mixed states.

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing).** Frozen periods count
  toward the tally; their status is rendered identically to the
  table pill (terminology matches).
- **AI-assist tone discipline.** The tally uses status terms
  literally (`in_progress`, `frozen`) — no marketing copy like "5
  audits in flight."

## Canvas references

- `Plans/mockups/audits.html` lines 107-122 (title bar block)
- `Plans/canvas/08-audit-workflow.md` — status vocabulary

## Dependencies

- **#204** — UI parity audit (surfacing parent)
- **#102** — audits page (the surface being extended)

## Anti-criteria (P0 — block merge)

- **P0-215-1.** Does NOT include statuses with count 0 in the
  rendered string. `0 closed` is noise.
- **P0-215-2.** Does NOT invent statuses outside the platform's
  enum. The tally renders only statuses present in `periods[].status`
  values, not the full enum.
- **P0-215-3.** Does NOT re-query the platform for the tally —
  derives from the same TanStack Query cache as the table.

## Skill mix (3-5)

1. React derived-value composition
2. Vitest formatter test
3. Playwright e2e assertion
