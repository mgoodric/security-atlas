- **feat(a11y):** slice 359 — skip-link in the authed layout. Closes
  slice 331 audit finding A11Y-1 (Critical). `web/app/(authed)/layout.tsx`
  renders a visually-hidden `<a href="#main-content">Skip to main
content</a>` as the first focusable element on every authed page; the
  link becomes visible on focus (Tailwind `sr-only` /
  `focus:not-sr-only`). `<main>` gains `id="main-content"` and
  `tabIndex={-1}` so the hash-link moves focus into the content region.
  WCAG SC 2.4.1 Bypass Blocks (Level A) + SC 2.4.7 Focus Visible.
  Keyboard-only users (RSI flare, motor impairment, screen-reader
  laptop) no longer tab through ~25 chrome affordances (TopBar +
  Sidebar) on every authed page navigation. Playwright assertion in
  `web/e2e/dashboard.spec.ts` pins the keyboard-reachability +
  focus-target contract: Tab once focuses the skip-link, Enter moves
  focus to `<main>`. ~5 LOC.
