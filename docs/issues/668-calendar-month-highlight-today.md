# 668 — Calendar month view does not highlight "today"

**Cluster:** Calendar
**Estimate:** XS (<0.5d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-014).

## Narrative

The compliance calendar month grid (`/calendar?view=month`) does **not** visually mark the
current day — a standard calendar affordance. Observed 2026-06-10: day 10 is not highlighted;
no `aria-current` / today class. Re-verified on `main` build `2a3805b`. Filed as an
enhancement (not a regression).

## Threat model

None — visual affordance only.

## Acceptance criteria

- [ ] **AC-1.** The current day's cell in the month grid is **visually highlighted** (a
      "today" treatment distinct from selection/hover).
- [ ] **AC-2.** The today cell carries **`aria-current="date"`** for assistive tech (a11y,
      consistent with the slice-331 audit discipline).
- [ ] **AC-3.** The highlight tracks the actual current date (not a hardcoded value);
      Playwright/unit asserts the today cell is marked.

## Anti-criteria

- Does NOT change calendar data, event rendering, or the week/agenda views beyond the today marker.

## Dependencies

- The compliance calendar month grid (`web/app/(authed)/calendar`).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-014** (priority low /
severity minor; type enhancement). Re-tested open on build `2a3805b`.
