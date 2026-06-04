# 359 — A11y skip-link to `<main>` in authed layout

**Cluster:** Frontend / a11y
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 331's a11y audit
(`docs/audits/331-a11y-wcag-audit.md` finding A11Y-1, severity
Critical) surfaced that the authed layout has no skip-link. A
keyboard-only user landing on any authed page tabs through ~25
chrome affordances — TopBar (mobile-sidebar trigger · logo link ·
breadcrumb · global search input · ⌘K kbd hint · in-progress audit
pill · tenant switcher · user avatar · sign-out form) + Sidebar (13
nav items + 2 count badges) — before reaching `<main>` content. On
every navigation.

For a keyboard-only user (RSI flare, motor impairment, or someone
using a screen reader on a laptop without a mouse), this transforms
simple page changes into a multi-minute keyboard marathon. WCAG SC
2.4.1 Bypass Blocks (Level A) requires a mechanism to bypass blocks
of content repeated on multiple pages. The project does not have
one.

The fix is a visually-hidden link that becomes visible on focus.
WCAG-canonical pattern; ~5 LOC.

### What ships

1. **Skip-link element.** First child inside the authed layout's
   outer `<div>` (`web/app/(authed)/layout.tsx`):

   ```tsx
   <a
     href="#main-content"
     className="sr-only focus:not-sr-only focus:absolute focus:top-4 focus:left-4 focus:z-50 focus:rounded-md focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:shadow-lg focus:outline-none focus:ring-2 focus:ring-ring"
   >
     Skip to main content
   </a>
   ```

2. **Anchor target.** Add `id="main-content"` and `tabIndex={-1}` to
   the `<main>` element so the link's hash navigation moves focus
   to the content region.

3. **Playwright spec.** Add a single assertion to a
   representative authed e2e spec (e.g.
   `web/e2e/dashboard.spec.ts`) that the skip-link is keyboard-
   reachable and moves focus to `<main>`. The slice 178 harness
   extension (slice 331 audit D8) may absorb this; if so, file as a
   harness-extension spillover.

### Why this matters

Critical-severity per the audit's user-impact tiering (slice 331
decisions D4): the chrome stop-count multiplied by every navigation
is the load-bearing impact, even though WCAG-level it's only A. The
fix is ~5 LOC; the impact is "the application becomes navigable for
keyboard-only users."

## Threat model

UI-chrome-only change. STRIDE pass:

- **S / T / R / D / E:** No surface changes.
- **I:** None.

## Acceptance criteria

- [ ] **AC-1.** Authed layout renders a visually-hidden skip-link as
      the first focusable element on every authed page.
- [ ] **AC-2.** Tabbing once from page load focuses the skip-link;
      pressing Enter (or Space) moves focus to `<main>`.
- [ ] **AC-3.** The skip-link is visible (not `sr-only`) when
      focused — meets WCAG 2.4.7 Focus Visible.
- [ ] **AC-4.** A Playwright spec asserts the skip-link's
      keyboard-reachability + focus-target behavior.
- [ ] **AC-5.** `pre-commit run --all-files` passes.

## Anti-criteria (P0 — block merge)

- **P0-359-1.** Does NOT add the skip-link to `/login` or other
  non-authed pages; the chrome problem is authed-only.
- **P0-359-2.** Does NOT modify any other a11y surface — this is
  the A11Y-1 slice only. A11Y-2/3/4/5 are separate slices.
- **P0-359-3.** Does NOT introduce a new dependency.
- **P0-359-4.** Does NOT change the TopBar or Sidebar internal
  structure.

## Dependencies

- **#331** (a11y audit) — `merged` (closing this slice).

## Notes

The slice 178 harness (`web/e2e-audit/`) may want a same-named
assertion that runs against every route. If lifted, file as the
harness extension via the slice 331 D8 mechanism — separate slice.
