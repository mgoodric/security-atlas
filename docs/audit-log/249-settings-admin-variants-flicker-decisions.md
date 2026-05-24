# Slice 249 — Settings admin-variants flicker decisions

**Slice:** `docs/issues/249-settings-admin-variants-flicker-on-first-paint.md`
**Status:** decisions captured during BUILD
**Author:** engineer (Claude / PAI Engineer agent)
**Date:** 2026-05-23

---

## D1 — Design option selected: Option 3 (initialData hydration via Next.js cache)

**Decision.** Option 3 (initialData hydration via `HydrationBoundary`)
selected over options 1 and 2.

**Implementation surface.** `web/app/(authed)/settings/layout.tsx` is
promoted from the slice-248 passthrough to a server-component
prefetch:

1. Reads the `atlas_jwt` cookie server-side via `cookies()` from
   `next/headers`.
2. Calls upstream `GET /v1/me` directly (not via the BFF route — avoids
   an unnecessary self-fetch hop) with the bearer as
   `Authorization: Bearer <jwt>`.
3. Projects the upstream JSON onto `SessionMe` via `parseSessionMe`
   (a fail-closed pure helper extracted to `admin-prefetch.ts`).
4. Seeds a per-request `QueryClient` cache under the same `queryKey`
   the page registers (`["settings-session-me"]`, exported as
   `SETTINGS_SESSION_ME_QUERY_KEY` so the constant cannot drift).
5. Wraps `children` in `<HydrationBoundary state={dehydrate(qc)}>`.

The page (`page.tsx`) stays `"use client"`; its
`useQuery(["settings-session-me"], getSessionMe)` reads the
prefetched value as initialData. The client-side `/v1/me` re-fetch
(P0-249-1) fires on the normal `staleTime` (60s default per
`lib/queryClient.tsx`) and stays the source-of-truth post-hydration.

---

### Rationale — why Option 3 over Option 1 (cookie-decode + Server-Component path)

- **Blast radius.** Option 1 refactors `page.tsx` away from
  `"use client"`. The page is ~1620 LOC and contains nine
  TanStack-Query hooks, six `useState`/`useReducer` calls, three
  modal dialogs, and a debounced PATCH mutation. Splitting it
  into server + client islands is a 200-LOC change at minimum
  and re-introduces hydration risk we don't have today.
- **Trust boundary clarity.** Option 1 wants to decode the JWT
  client-side and trust the decoded claims directly. The platform's
  `/v1/me` re-verification (called via the BFF on every page load)
  is the existing trust boundary; introducing a second decode path
  doubles the surface area without buying additional safety.
- **Slice 209 composition.** Slice 209 plumbs the local-credential
  JWT; Option 3 consumes the SAME cookie (`atlas_jwt`,
  `SESSION_COOKIE`) without needing to know whether the bearer is
  an OIDC token or a local-credential token — the platform decides.

### Rationale — why Option 3 over Option 2 (skeleton-until-hydrated)

- **Trades one flicker for another.** Option 2 renders a skeleton
  for the role-aware sections until `meQuery.data` resolves. The
  primary-user persona sees a different flicker (skeleton → admin
  variant) instead of the wrong-variant flicker — still a flicker.
  The slice spec explicitly flags this as not solving the problem.
- **Slowest first paint.** Every settings page load shows a
  skeleton for the role-aware regions even when the role data could
  be known at SSR. For an admin user (the primary persona) this is
  strictly worse than today.
- **Loading-state proliferation.** Three of the page's six sections
  would gain skeleton variants; the maintenance surface grows for
  no UX win.

### Rationale — why Option 3 specifically

- **Smallest diff.** Three new files (`admin-prefetch.ts`,
  `admin-prefetch.test.ts`, this decisions log), one updated layout
  (`layout.tsx`), one extended e2e spec, one CHANGELOG bullet. The
  page itself is untouched.
- **Composes with slice 248.** The layout-as-server-component
  pattern already exists for the `<title>` metadata; we extend the
  same surface with the prefetch.
- **Composes with the existing TanStack Query SSR pattern.**
  `lib/queryClient.tsx` already implements the canonical "fresh
  client per server request, singleton in the browser" pattern.
  `HydrationBoundary` + `dehydrate` are the docs-blessed primitives
  for handing prefetched data to a client `useQuery`.
- **Failure modes converge with the post-hydration re-fetch.** Both
  paths call upstream `/v1/me` with the same bearer; identical
  inputs yield identical outputs. There is no scenario where the
  prefetch admits an "admin" while the post-hydration re-fetch
  demotes — the platform is the single source of truth.

---

## D2 — Failure modes (P0-249-3 fail-closed)

**Decision.** Every failure mode of the server-side prefetch returns
`NON_ADMIN_SESSION_ME = { is_admin: false }`. The SSR ships the
non-admin variant; the client-side re-fetch is the recovery path
when the platform later succeeds.

| Failure mode              | Behaviour                                      |
| ------------------------- | ---------------------------------------------- |
| `atlas_jwt` cookie absent | `NON_ADMIN_SESSION_ME` (no upstream fetch)     |
| Upstream `/v1/me` 401     | `NON_ADMIN_SESSION_ME`                         |
| Upstream `/v1/me` 403     | `NON_ADMIN_SESSION_ME`                         |
| Upstream `/v1/me` 5xx     | `NON_ADMIN_SESSION_ME`                         |
| Upstream returns non-JSON | `NON_ADMIN_SESSION_ME` (caught in try/catch)   |
| Upstream returns null     | `NON_ADMIN_SESSION_ME` (parseSessionMe branch) |
| Upstream returns `{}`     | `NON_ADMIN_SESSION_ME` (parseSessionMe branch) |
| Network throw             | `NON_ADMIN_SESSION_ME` (try/catch)             |
| `is_admin === true`       | `{ is_admin: true }` — the admit case          |
| `is_admin === "true"`     | `NON_ADMIN_SESSION_ME` (strict equality only)  |
| `is_admin === 1`          | `NON_ADMIN_SESSION_ME` (strict equality only)  |

The narrow accept set ("only literal boolean `true` is admin") is
identical to the slice 060 BFF route's posture
(`route.ts` line 77: `const isAdmin = body.is_admin === true;`).
The vitest suite (`admin-prefetch.test.ts`) covers each branch.

---

## D3 — Per-request QueryClient (P0-249-4 no cross-user cache)

**Decision.** Use `getQueryClient()` from `lib/queryClient.tsx`. The
function's SSR branch (`typeof window === "undefined"`)
unconditionally calls `makeQueryClient()` — a FRESH client per
server render. The browser-singleton branch is never exercised
during SSR.

Each `SettingsLayout` invocation gets its own `QueryClient` whose
prefetched cache is serialized into the response HTML and tied to
that response only. A subsequent request from a different user
goes through a brand-new render with a brand-new `QueryClient`;
there is no shared state.

This honors P0-249-4 (no `/v1/me` caching across users/sessions)
without any additional code — the existing `getQueryClient()`
factory already does the right thing for App-Router SSR.

---

## D4 — Vitest coverage shape (AC-6 honoring slice 069 P0-A3)

**Decision.** Vitest tests live at
`web/app/(authed)/settings/admin-prefetch.test.ts` and cover the
pure-logic surface (`parseSessionMe`, `NON_ADMIN_SESSION_ME`,
`SETTINGS_SESSION_ME_QUERY_KEY`). They do NOT render the page.

**Why not render the page in vitest.** Slice 069 P0-A3 explicitly
prohibits a `@testing-library/react` dependency: the vitest config
is node-env, no JSX rendering, `*.test.ts` only (no `.test.tsx`).
The configured `include` pattern excludes `.tsx` test files.
Introducing a React-render test here would violate that discipline.

**Why this honors the AC-6 intent.** The fail-closed correctness
of `parseSessionMe` is the safety-critical surface; that's what
AC-6 cares about ("first-paint variant assertion" = correct admit
decision at SSR time). The page-render assertion lives in
Playwright (AC-14), where SSR HTML is fetched directly and
inspected — a much stronger test than a JSDOM-mocked render.

The vitest suite asserts:

- Admit set: `{ is_admin: true }` → admin (and ignores other fields).
- Fail-closed: `null`, `undefined`, non-object, missing field, empty
  object, string `"true"`, number `1`, explicit `null`, explicit
  `false` all map to non-admin.
- Query-key drift guard: `SETTINGS_SESSION_ME_QUERY_KEY` ===
  `["settings-session-me"]` (binds the layout's prefetchQuery to the
  page's useQuery).
- Constant drift guard: `NON_ADMIN_SESSION_ME` === `{ is_admin: false }`.

---

## D5 — Playwright AC shape (AC-7 / spec AC-14)

**Decision.** The new e2e AC reads the SSR HTML directly via
`page.request.get("/settings")` rather than `page.goto()`.

**Why.** `page.goto()` lets client JS execute; by the time
Playwright inspects the DOM, the hydration step has already run
and the page swap has already happened — masking the exact
regression we're guarding against. `page.request.get()` performs
a raw HTTP GET with the test context's cookies and returns the
response body verbatim. That body is the SSR-only HTML, the
property we actually want to assert about.

The assertion set:

- Status code 200.
- Positive: HTML contains `data-testid="settings-admin-cross-link"`.
- Negative: HTML does NOT contain
  `data-testid="settings-section-tokens-non-admin"`.
- Negative: HTML does NOT contain literal "Admin role required".
- Negative: HTML does NOT contain
  "Tenant administration (admin role required)".

The seed bearer is `is_admin=true` (slice 082 harness), so the SSR
response must show the admin variant.

---

## D6 — Layout retains slice 248 metadata export

**Decision.** The `metadata` export from slice 248 stays in place;
this slice extends the layout with the prefetch logic but does NOT
remove or alter the page-`<title>` metadata.

This preserves AC-13 of `settings.spec.ts` (slice 248 close-out) so
the 11/11 existing ACs continue to pass (slice 249 AC-8 / P0-249-5).

---

## Constitutional invariants honored

| Invariant                                  | How                                                                          |
| ------------------------------------------ | ---------------------------------------------------------------------------- |
| **P0-249-1** keep client `/v1/me` re-fetch | `page.tsx` unchanged; `useQuery(getSessionMe)` still fires on hydrate        |
| **P0-249-2** no server-side authz change   | `internal/api/admincreds/` untouched (AC-5 verified by `git diff`)           |
| **P0-249-3** fail-closed when JWT absent   | Every failure path returns `NON_ADMIN_SESSION_ME`; vitest covers each branch |
| **P0-249-4** no cross-user `/v1/me` cache  | `getQueryClient()` SSR branch returns a fresh client per request             |
| **P0-249-5** preserve 11/11 settings ACs   | Layout extends, does not replace; slice 248 metadata preserved (D6)          |

---

## File manifest

- **NEW** `web/app/(authed)/settings/admin-prefetch.ts` — pure-logic helpers (`parseSessionMe`, constants).
- **NEW** `web/app/(authed)/settings/admin-prefetch.test.ts` — vitest coverage of every fail-closed branch.
- **CHANGED** `web/app/(authed)/settings/layout.tsx` — promoted from passthrough to server-component prefetch.
- **CHANGED** `web/e2e/settings.spec.ts` — adds AC-14 (no admin-variant flicker on SSR).
- **CHANGED** `CHANGELOG.md` — `### Fixed` bullet under `## [Unreleased]`.
- **NEW** `docs/audit-log/249-settings-admin-variants-flicker-decisions.md` — this file.
