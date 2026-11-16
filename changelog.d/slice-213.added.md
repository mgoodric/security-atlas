- **frontend:** slice 213 — audits header chrome (subset). Closes the
  parity gap surfaced by the slice 204 audit fleet: the live `/audits`
  page's topbar carried only the brand mark + sign-out, while the
  mockup at `Plans/mockups/audits.html` shows four additional
  affordances. This slice ships TWO of the four — the **in-progress
  audit pill** (amber pill reading "<period name> in progress",
  client component over `/api/audits` with 60s stale via TanStack
  Query, returns null on zero matches per P0-213-2 silent-absence)
  and the **user avatar** (initials + display name, server component
  over `/api/me` with fail-closed-on-error per the slice 186 sidebar
  pattern). Both are mounted in the shared authed-shell topbar
  (`web/components/shell/topbar.tsx`) so the chrome ships globally,
  not just on `/audits`. The remaining two affordances (breadcrumb,
  global cmd-K search) are filed as spillover slices 271 and 272
  respectively — see
  `docs/audit-log/213-audits-header-chrome-decisions.md` D1 for the
  rationale. New unit tests (`web/lib/display-name.test.ts` × 14,
  `web/components/shell/in-progress-audit-pill.test.ts` × 6) pin the
  pure helpers; new Playwright spec (`web/e2e/audits-header.spec.ts`)
  covers the integrated positive case + the P0-213-1 "no new
  endpoint" assertion. Fixture
  `fixtures/e2e/audits-header.sql` seeds one in_progress period and
  the demo user row. AC-1 maintainer JUDGMENT call recorded in
  `docs/audit-log/213-audits-header-chrome-decisions.md`.
