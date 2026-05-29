# 380 — Dashboard Server Component fan-out + parallel data fetch (close slice 332 F-BFF-2)

**Cluster:** Performance / Frontend
**Estimate:** 1.5d
**Type:** JUDGMENT (the right server-side fan-out pattern requires
canvas-vs-Next.js-15-AppRouter trade-off discussion)
**Status:** `ready`

## Narrative

Closes slice 332 finding **F-BFF-2 (MEDIUM)**. The dashboard page
(`/dashboard`) renders multiple panels — activity, freshness, drift,
upcoming, risks, framework-posture — each backed by its own BFF
route in `web/app/api/dashboard/*/route.ts`. Each panel today fetches
client-side via TanStack Query `useQuery`. Page-level "time to
interactive" is bounded by the slowest of the N panels in parallel
(TanStack Query parallelizes within a single React tree, but every
panel still pays its own bearer-cookie-read + Next.js BFF overhead +
upstream HTTP).

A Server Component dashboard page that fetches all panels in parallel
server-side via `Promise.all` collapses the N round-trips into 1
network round-trip for the user (the initial HTML response), with
server-side streaming for late-arriving panels.

At v1 1–10 concurrent users this is fine; at v2 50+ concurrent users
the 7× upstream-request amplification per dashboard load adds load
to the eval engine that slice 377's prepared-query cache would
otherwise free up. The two slices compose.

### Why now

Slice 332 surfaced this as the dashboard page's structural
inefficiency. The Next.js 16 App Router (verified pinned at slice 078)
supports Server Component streaming natively. The work is mostly
re-shaping `web/app/dashboard/page.tsx` and friends.

### Trigger

Slice 332 performance audit, surface 5 (Frontend BFF), finding
F-BFF-2.

### Disposition

Code change to `web/app/dashboard/` only. BFF routes remain as a
client-fetchable surface (for the per-panel refresh affordance), but
the initial page load fetches server-side.

## Threat model

Server Component data fetch shape change. STRIDE:

- **S:** Bearer-cookie is read server-side (same as the BFF routes
  do); no change to auth surface.
- **T:** No new tampering surface.
- **R:** Initial-page-load audit trail surfaces server-side instead
  of as N independent BFF requests.
- **I:** No information disclosure.
- **D:** **Slightly load-bearing.** A slow panel (e.g. a slow drift
  query) blocks the Server Component stream. Mitigation: each panel
  is its own Suspense boundary so a slow panel renders its skeleton
  while the rest stream in. No worse than the current per-panel
  loading state.
- **E:** No new privilege boundary.

**Constitutional invariants honored**: tenant isolation is unchanged
— the platform derives tenant from the bearer credential server-side.
The Server Component path uses the same bearer-cookie + upstream
fetch shape.

## Acceptance criteria

- [ ] **AC-1.** `web/app/dashboard/page.tsx` becomes a Server
      Component that fetches all panel data in parallel via
      `Promise.all`.
- [ ] **AC-2.** Each panel renders inside a `<Suspense fallback={
<PanelSkeleton/> }>` so slow panels don't block fast ones.
- [ ] **AC-3.** The existing per-panel BFF routes (`web/app/api/
dashboard/*/route.ts`) REMAIN as a fetchable surface — used
      for client-side refresh after navigation. NOT removed.
- [ ] **AC-4.** A Playwright e2e spec at `web/e2e/dashboard-
server-component.spec.ts` asserts: the dashboard page renders
      all panels' initial content WITHOUT a client-side fetch
      occurring (asserted via network-request count == 1 for the
      initial HTML).
- [ ] **AC-5.** TanStack Query still hydrates with the
      server-fetched data so client-side refresh works correctly.
- [ ] **AC-6.** A vitest spec asserts the server-side data-fetcher
      function returns the same shape the client `useQuery` hook
      previously expected.
- [ ] **AC-7.** No regression to existing `web/lib/api.ts` typed
      client (the Server Component reuses the same client functions
      that the BFF routes call).
- [ ] **AC-8.** Tail-latency improvement: a Playwright trace
      captures initial-page-load network waterfall pre-slice and
      post-slice; post-slice has 1 initial request vs pre-slice 7+.
- [ ] **AC-9.** `pre-commit run --files` passes.

## Anti-criteria (P0)

- **P0-1.** Does NOT remove the per-panel BFF routes — they remain
  for client-side refresh.
- **P0-2.** Does NOT cache the server-fetched data in the Next.js
  data cache (`cache: "no-store"` MUST stay — slice 040 / slice 332
  F-BFF-1 confirmed this is correct for session-bearing routes).
- **P0-3.** Does NOT bypass the bearer-cookie read on the
  server-side fetcher — the same `lib/auth SESSION_COOKIE` cookie
  jar must be the source of the upstream Authorization header.
- **P0-4.** Does NOT change the wire shape of any BFF route — the
  client still expects the same JSON envelope per panel.
- **P0-5.** Does NOT auto-merge.

## Dependencies

- **#332** (performance audit) — `merged`. Source finding.
- **#040** (dashboard panel BFFs) — `merged`. Owner of the BFF route
  surface.
- **#066** (dashboard backend endpoints) — `merged`. The upstream
  /v1 endpoints.
- **#078** (Next.js 16 pin) — `merged`. The Server Component
  streaming substrate.

## Notes for the implementing agent

1. The Server Component path uses `cookies()` from `next/headers`
   server-side — same primitive the BFF route handlers use.
2. The slowest panel in the v1 dataset is likely
   `framework-posture` (multi-framework rollup). Make it its own
   `<Suspense>` so its slowness doesn't block the other 6.
3. Re-use `lib/api.ts`'s typed client functions — do NOT
   reimplement the fetcher. The whole point is the Server Component
   path is a thin server-side wrapper around the same client.
4. TanStack Query has a `dehydrate`/`hydrate` shape for SSR. Use it
   so post-hydration client-side refresh hits the same `useQuery`
   cache key.
5. Consider whether the per-panel BFF routes should set
   `Cache-Control: no-store` explicitly on the Response (currently
   implicit). Document either way in the decisions log.
