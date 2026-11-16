- **frontend:** slice 249 — `/settings` admin variants no longer flicker
  through the non-admin variant on first SSR paint. The page's
  `useQuery(["settings-session-me"], getSessionMe)` had not resolved at
  SSR time, so an admin user briefly saw "Admin role required",
  `data-testid="settings-section-tokens-non-admin"`, and the muted
  "Tenant administration (admin role required)" span before the
  client-side `/v1/me` re-fetch swapped variants — ~50-200ms of wrong
  UI for the primary-user persona who IS the admin. Fix:
  `web/app/(authed)/settings/layout.tsx` is promoted from the slice-248
  passthrough to a server-component prefetch that reads the `atlas_jwt`
  cookie, calls upstream `GET /v1/me` server-side, and hands the result
  to the page via TanStack Query's `<HydrationBoundary>` /
  `dehydrate(queryClient)` primitives. The page (`page.tsx`) stays
  `"use client"`; its `useQuery` reads the prefetched value as initial
  data and the SSR HTML now ships the correct admin/non-admin variant
  on the first byte. The client-side `/v1/me` re-fetch (P0-249-1) is
  preserved as the source-of-truth post-hydration. Fail-closed posture
  honored on every failure mode (cookie absent / non-200 / non-JSON /
  network throw -> non-admin variant; P0-249-3). Per-request
  `QueryClient` from the existing `lib/queryClient.tsx` factory means
  no cross-user cache (P0-249-4). New pure-logic helper at
  `web/app/(authed)/settings/admin-prefetch.ts` (vitest-covered, every
  fail-closed branch); new Playwright AC-14 in `web/e2e/settings.spec.ts`
  asserts the raw SSR HTML via `page.request.get("/settings")` contains
  the admin cross-link and contains neither "Admin role required" nor
  the non-admin tokens-section testid. The server-side authz gate at
  `internal/api/admincreds/` is untouched (AC-5; verified by `git diff`)
  — UI chrome is not the access decision, the platform's per-request
  authz on `/v1/admin/credentials` remains the trust boundary. Decision
  log: `docs/audit-log/249-settings-admin-variants-flicker-decisions.md`
  (D1 records the choice of Option 3 -- initialData hydration via
  HydrationBoundary -- over Option 1 (cookie-decode + Server-Component
  refactor; rejected for blast radius) and Option 2 (skeleton-until-
  hydrated; rejected because it trades one flicker for another)). Slice
  `docs/issues/249-settings-admin-variants-flicker-on-first-paint.md`.
