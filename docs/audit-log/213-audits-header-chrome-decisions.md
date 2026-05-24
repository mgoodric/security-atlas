# Decisions log — slice 213 (audits header chrome parity gap)

**Slice:** [`docs/issues/213-audits-header-chrome-parity-gap.md`](../issues/213-audits-header-chrome-parity-gap.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-23

---

## Summary

Slice 213 closes a header chrome parity gap surfaced by the slice 204
audit fleet: the live `/audits` page's topbar carried only the brand
mark + sign-out, while the mockup at `Plans/mockups/audits.html`
showed four additional affordances (breadcrumb, in-progress audit
pill, global search, user avatar). Per AC-1 the maintainer JUDGMENT
call was to pick a subset that ships in this slice and defer the rest
to follow-ons.

This log captures the JUDGMENT calls. The slice spec is the source of
intent; this log is the source of "what we actually decided when the
spec said `your call`".

---

## D1 — Subset shipped in this slice

**Decision:** ship the **in-progress audit pill** + the **user
avatar**. Defer the **breadcrumb** + **global search** to spillover
slices 271 + 272.

**Rationale (why these two and not the others):**

| Element          | Ship now? | Reason                                                                                                                                                                                                                                                                                                                                                                  |
| ---------------- | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| In-progress pill | YES       | Narrow surface. Backing data already on the wire (`/api/audits` from slice 102). High-signal UX cue (operator sees "Q2 in progress" at a glance). Fits within one component. Strong test isolation — pure filter helper is unit-coverable, integrated render is e2e-coverable.                                                                                          |
| User avatar      | YES       | Narrow surface. Backing data already on the wire (`/api/me` from slice 108). Identity affordance is load-bearing for multi-user tenants (slice 192). Fits within one server component. Pure derivation helpers (`deriveDisplayName`, `deriveInitials`) are unit-coverable; integrated render is e2e-coverable.                                                          |
| Breadcrumb       | NO        | Cross-page surface. Every authed page has a different page name to fill in; the same chip appears on every mockup. Inventing the cross-page pattern in a single-page JUDGMENT slice would be premature. Filed as **slice 271** — a `Breadcrumb` component in the shared topbar reading tenant name (existing `/v1/me/tenants`) + page name (derived from URL segments). |
| Global search    | NO        | Substantive product feature, not chrome. Requires a `POST /v1/search` backend endpoint (filed as **slice 268**, not yet ready) + a real cmd-K modal (Linear / Stripe / Vercel pattern). A stub modal would be worse than no surface — operators would type, see "coming soon", lose trust. Filed as **slice 272**, marked `not-ready` until 268 ships.                  |

**What this resolves:** the spec's AC-1 explicitly grants maintainer
JUDGMENT to pick a subset. The spec recommended exactly the subset I
chose. I followed the recommendation because the rationale held up
under independent analysis — the alternative subset (e.g. ship the
breadcrumb in this slice) would have pulled in cross-page surfaces
that are properly the responsibility of a dedicated breadcrumb slice.

**Spillover slices filed:**

- [`271-shared-shell-breadcrumb.md`](../issues/271-shared-shell-breadcrumb.md) — `ready`, 1.0d
- [`272-global-search-cmdk.md`](../issues/272-global-search-cmdk.md) — `not-ready` (blocked on slice 268)

---

## D2 — In-progress pill: client component, TanStack Query, fail-quiet

**Decision:** the pill is a **client component** using **TanStack
Query** with a **60s stale time**. On loading / error / zero-match,
the component returns `null` (no copy, no spinner).

**Rationale:**

- The page's audit table already uses `useQuery` against `/api/audits`
  with `queryKey: ["audits", "list"]`. Using the same query key from
  the pill means the pill **shares the cache** — on `/audits` the
  pill renders from the same fetch the table uses (no second network
  call). On other pages (e.g. `/dashboard`) the pill fires its own
  fetch.
- 60s stale time per AC-3 explicit ask. Fresh enough to surface the
  amber dot soon after a period transitions to `in_progress`, slow
  enough not to hammer the BFF.
- Loading state intentionally renders `null` rather than a skeleton
  pill. A loading skeleton in the topbar chrome would shift layout on
  every page load — a brief gap is the lesser UX evil.
- Error state renders `null` per P0-213-2 (silent absence is honest)
  and to keep chrome from broadcasting unrelated 5xx noise.

**What this resolves:** spec did not specify server-vs-client. The
client + shared-query-cache choice is the unambiguously cheaper
architecture.

---

## D3 — User avatar: server component, fail-closed (mirror slice 186)

**Decision:** the avatar is a **server component** that fetches
`/api/me` via the bearer cookie inside its render. On any failure
(missing bearer, non-200, parse error, missing fields), it returns
`null`.

**Rationale:**

- This is the **exact pattern** slice 186 (sidebar admin role gate)
  established and the codebase has lived with for weeks (see
  `web/components/shell/sidebar.tsx` `fetchAdminMe`). Reusing the
  pattern keeps mental load low and the fail-closed behavior is
  already familiar to reviewers.
- The pure derivation helpers (`deriveDisplayName`, `deriveInitials`)
  are factored out to `web/lib/display-name.ts` so they're unit-
  testable without the React / Next.js machinery (slice 186 did the
  same split with `lib/admin-nav.ts`).
- Fail-closed: better a brief gap during the initial fetch than the
  wrong identity rendered confidently. Parallels P0-186-4.

**What this resolves:** spec did not specify server-vs-client for the
avatar. The server-component + fail-closed choice mirrors the latest
constitutional pattern.

---

## D4 — Display-name fallback chain

**Decision:** display name resolves as `display_name → email
local-part → empty string`. When empty, the avatar renders nothing
(component returns `null`).

**Rationale:** AC-4 says "falls back to the email's local-part if
`name` is unset". I treated "unset" as ALSO covering whitespace-only
strings (the slice 108 backend does not normalize, so a user with
`display_name = "   "` would otherwise render an empty initials
circle). The trim is documented in the unit test and in the helper's
JSDoc.

The empty fallback (when neither display_name nor email resolve)
returns `null` from the component, mirroring the fail-closed posture.
This case shouldn't happen in practice — `/v1/me` returns `email` as
a required field — but the defense is cheap.

**What this resolves:** AC-4 was silent on the whitespace-only case.

---

## D5 — Pill icon style is the mockup's amber palette + dark-mode pair

**Decision:** the pill uses Tailwind's `amber-50 / amber-200 /
amber-800` palette for light mode (matches the mockup at lines 38-40
of `Plans/mockups/audits.html`) plus a `dark:` companion (`amber-950
/ amber-900 / amber-300`) for dark mode.

**Rationale:** the mockup was built before the slice 170 dark-mode
work landed. Shipping just the light palette would render unreadable
on dark-mode (high-contrast amber-800 text on a near-black
background → AA contrast fail). The dark-mode companion uses the
same Tailwind palette shifted to higher-contrast shades for the
darker background.

**What this resolves:** mockup was a single-mode design; dark-mode
pairing is a build-time JUDGMENT call.

---

## D6 — E2E spec scope: positive case only, vitest covers the null case

**Decision:** the Playwright spec asserts the **positive case** (pill
visible, copy correct, avatar visible, name + initials correct). The
**"pill absent when zero in_progress periods"** case is covered by
the vitest sibling on the pure `pickMostRecentInProgress` helper, not
by the e2e spec.

**Rationale:** asserting "pill absent" in Playwright requires either
(a) a separate fixture with zero in_progress periods that gets
applied to a different test bearer, or (b) DELETEing the in_progress
period mid-spec. Both add fixture complexity for marginal coverage
gain — the truthful invariant ("zero matches → render null") is
isolated in the picker function and unit-tested directly.

**What this resolves:** AC-5 reads "absent otherwise"; the unit-test
sibling discharges that obligation.

---

## D7 — New fixture file vs. extending an existing one

**Decision:** add `fixtures/e2e/audits-header.sql` as a **new fixture
file**, register `"audits-header"` in the `FixtureName` union in
`seed.ts`.

**Rationale:** the existing `audit-workspace.sql` seeds a `frozen`
period. Adding the `in_progress` row to that fixture would couple two
unrelated specs (audit-workspace spec asserts frozen-period
behaviors; adding an in_progress row would silently mutate its
visible state on every run). The slice 082 harness pattern is "one
fixture per spec"; this slice adheres to that pattern.

**What this resolves:** harness pattern was unambiguous; the build
choice was whether to merge into an existing fixture or branch a new
one. Branching is the constitutional move.

---

## D8 — No changes to `_STATUS.md`

**Decision:** this slice does NOT modify
`docs/issues/_STATUS.md`. The loop orchestrator owns canonical row
flips.

**Rationale:** spec hard rule. No JUDGMENT involved — this is the
agreed division of labor between worktree agents and the
orchestrator.

---

## CI-delta scan

No CI-delta concerns:

- New files only (`web/components/shell/in-progress-audit-pill.tsx`,
  `web/components/shell/user-avatar.tsx`,
  `web/lib/display-name.ts`, plus tests + fixture + spec).
- `web/components/shell/topbar.tsx` modified to mount the two new
  components and turned `async` (server component already; the
  parent layout already awaits it implicitly via JSX).
- `web/e2e/seed.ts` extended with the `"audits-header"` literal in
  the `FixtureName` union (additive; no rename, no removal).

Local CI parity verified before push:

- `pre-commit run --all-files` — green
- `npm run lint` (web) — green (2 pre-existing warnings in
  `scripts/capture-readme-screenshots.ts`; unrelated)
- `npm run test` (web) — 88 files / 928 tests / all green (incl.
  the 14 new `display-name.test.ts` tests + the 6 new
  `in-progress-audit-pill.test.ts` tests)
- `npx tsc --noEmit` (web) — 15 errors, identical baseline to main
  (pre-existing in `scripts/capture-readme-screenshots.test.ts` and
  `lib/auth/oauth-client.test.ts`); no new errors from this slice
- `npm run build` (web) — green; all 30+ routes compile cleanly
- CHANGELOG bullet added under `## [Unreleased]` → `### Added`

The Playwright e2e spec (`audits-header.spec.ts`) requires the
slice-082 seed harness + a running platform; it is exercised in CI
by the `Frontend · Playwright e2e` job after the docker-compose
bring-up.
