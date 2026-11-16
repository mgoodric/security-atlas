- **frontend:** slice 277 — mobile-responsive baseline (viewport meta +
  sidebar drawer + per-page audit). The Next.js 16 App Router `viewport`
  export is added to the root layout (`web/app/layout.tsx`) — without it,
  mobile browsers fell back to the legacy ~980px desktop-emulation width
  and zoomed out, making the UI microscopic on phones even on pages
  whose Tailwind classes would have produced a perfectly usable mobile
  layout. The shared authed shell now collapses the `<Sidebar>` to a
  hamburger-triggered `<Sheet>` drawer at viewport widths `< md` (768px);
  at `≥ md` the inline sidebar renders exactly as pre-277 (P0-277-1 —
  no desktop UX regression). The drawer hosts the same nav items as the
  desktop sidebar (including slice 214's count badges), closes on
  Escape / outside-click / nav-click, and traps focus while open via
  the WAI-ARIA dialog primitive (`@base-ui/react/dialog` — no new
  top-level dep per P0-277-8). Two new docs ship: `web/docs/responsive-
discipline.md` (the breakpoint contract + three-checkpoint test rule +
  ban-list + slice-precedents rubric a new contributor follows) and
  `docs/responsive-audit.md` (per-route verdict table covering authed +
  admin routes; the source of truth for the long-tail per-page
  spillover slices). Spillover slice 281 filed against the priority
  three list-table pages (`/controls` + `/risks` + `/evidence`) that
  horizontal-scroll at 375px today. Playwright spec at
  `web/e2e/mobile-baseline.spec.ts` pins the foundational chrome
  behavior at 375px + 1280px. Decisions log:
  `docs/audit-log/277-mobile-responsive-baseline-decisions.md`
  (D1 sheet primitive · D2 md=768 breakpoint · D3 audit-doc shape ·
  D4 drawer nav-data shape · D5 topbar items stay in topbar ·
  D6 inline-SVG hamburger). Slice 277 spec landed via PR #623 at
  `3a859cd`; implementation lands via this PR.
