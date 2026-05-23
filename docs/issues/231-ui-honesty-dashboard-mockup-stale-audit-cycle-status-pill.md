# 231 — UI parity: dashboard mockup-stale "SOC 2 Type II · Q2 2026 in progress" topbar status pill

**Cluster:** Quality / UI parity (mockup hygiene)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during the slice 204 per-page UI parity audit fleet (page slug: `dashboard`; mockup file: `Plans/mockups/dashboard.html`). Category (iv) mockup-stale.

The dashboard mockup's topbar (`Plans/mockups/dashboard.html` lines 38–42) renders a persistent pill-shaped status indicator left of the search bar:

```html
<div
  class="flex items-center gap-2 px-2.5 py-1 bg-amber-50 border border-amber-200 rounded-full"
>
  <span class="w-1.5 h-1.5 bg-amber-500 rounded-full animate-pulse"></span>
  <span class="text-xs font-medium text-amber-800"
    >SOC 2 Type II · Q2 2026 in progress</span
  >
</div>
```

It's the only mockup element that surfaces audit-period state in the global chrome — implying the topbar should always communicate "is an audit currently running, and if so which".

A codebase grep for `"Q2 2026 in progress"`, `"audit-cycle"`, or a topbar-rendered AuditPeriod state turns up zero matches in `web/components/shell/topbar.tsx` or any sibling chrome component. The only matches are unrelated (a placeholder string in `web/app/(authed)/audits/new/audit-period-form.tsx`). **No code path exists for a persistent topbar audit-period status pill.**

There are two possible resolutions:

1. **MOCKUP-STALE** — the mockup encodes a design intent we have deferred. The audit-period UX lives at `/audits` (slice 030 + slice 042) and the dashboard mockup's pill is a one-off design idea that the team did not commit to building. Remove the pill from the mockup.
2. **SHIP-GAP** — the topbar pill is a valid affordance and we just haven't built it. File a separate ship slice.

**The audit's recommendation is (1) MOCKUP-STALE**, because: (a) the audit-period state is already surfaced inside the `/audits` workspace (slice 042's audit-period card), (b) duplicating it in the topbar adds a state-sync responsibility (which audit-period is "current"? a tenant may run two concurrent — see slice 030's design), (c) the mockup's `SOC 2 Type II · Q2 2026 in progress` is a single-string encoding that doesn't scale to multiple concurrent audit-periods. The slice-204 audit's judgment is that the design intent matures inside the audit workspace, not in global chrome.

**Why `ready`.** Mockup-only edit. No production-code change. Aligns with slice 183's precedent (which removed two stale mockup entries — Vendors anchor, Admin anchor — when the production sidebar diverged).

## Threat model

**S/T/R/I/D/E.** N/A — mockup file edit only. No deployed surface, no auth, no data.

**Verdict.** **no-mitigations-needed.**

## Acceptance criteria

- **AC-1.** The `<div>` block at `Plans/mockups/dashboard.html` lines 38–42 (the amber audit-cycle status pill) is removed.
- **AC-2.** An HTML comment is inserted at the removal site explaining the deletion, citing slice 231 and the slice 183 precedent (same removal pattern). Format:
  ```html
  <!-- Slice 231 (MOCKUP-STALE): the persistent "SOC 2 Type II · Q2 2026 in progress" topbar status pill was removed. Audit-period state surfaces inside the /audits workspace (slice 042's audit-period card), not in global chrome — duplicating it created a state-sync problem with multi-concurrent audit-periods (slice 030 design). Re-add behind a backing data path if the topbar truly needs the affordance. -->
  ```
- **AC-3.** The mockup still renders (no broken Tailwind class chains, no orphan closing tags).
- **AC-4.** The `docs/audit-log/204-page-audit-dashboard.md` finding entry F-204D-4 is updated to cite this slice's resolution.

## Constitutional invariants honored

- **Slice 178 honesty principle.** The mockup is design-doc reference; when the production design diverges (audit-period state belongs in the audit workspace), the mockup follows the production design, not the other way around.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.4 — audit-period freezing lives in the audit workspace.
- `Plans/canvas/01-vision.md` — anti-pattern "vanity trust centers" rejection extends to vanity status pills.

## Dependencies

- None. Mockup-only edit.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT add a topbar status pill to production. The audit's recommendation is removal, not implementation.
- **P0-A2.** DOES NOT remove the audit-period card from `/audits` (slice 042). That's the legitimate surface; this slice only removes the duplicate topbar mockup element.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-4.
