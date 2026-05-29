// Slice 380 -- server-side fan-out prefetch helpers for /dashboard.
// Closes slice 332 finding F-BFF-2 (MEDIUM).
//
// Why this module exists:
// `web/app/(authed)/dashboard/page.tsx` is a client component. Its six
// panels each register a `useQuery(["dashboard", <panel>], fetchDashboard*)`
// that, on a cold first load, fires a client-side request to the
// per-panel BFF route under `/api/dashboard/**`. Each BFF round-trip
// pays its own bearer-cookie-read + Next.js BFF hop + upstream HTTP, so
// page time-to-interactive is bounded by the slowest of N parallel
// client round-trips PLUS the BFF amplification (slice 332 F-BFF-2).
//
// The fix mirrors slice 249's `/settings` prefetch precedent: a sibling
// server-component `layout.tsx` reads the bearer cookie once, fetches
// all six panels' data in parallel server-side (`Promise.all`), seeds a
// per-request QueryClient under the EXACT keys the page registers, and
// ships the dehydrated cache in the initial HTML via `HydrationBoundary`.
// The client `useQuery` hooks then boot already-populated -- zero
// client-side BFF requests on first load, one network round-trip for
// the user (the HTML document itself). Slice 249 prefetched ONE query;
// slice 380 generalizes that to a six-query parallel fan-out.
//
// What this module is NOT (anti-criteria):
//   - It does NOT remove the per-panel BFF routes (P0-1). They remain
//     the client-side refresh surface; this module is a hydration
//     prime, not a replacement.
//   - It does NOT change any BFF wire shape (P0-4). The prefetched
//     value is the SAME shape the client `fetchDashboard*` fn returns,
//     so the dehydrated cache is wire-compatible with the page's
//     `useQuery`.
//   - It does NOT bypass the bearer-cookie read (P0-3). The layout reads
//     `SESSION_COOKIE` server-side and the upstream fns set the
//     Authorization header from it (same as the BFF proxy).
//   - It does NOT cache the server-fetched data (P0-2). `apiFetch` sets
//     `cache: "no-store"` (slice 380 hardening) so the upstream answer
//     is never cached across users/requests.
//
// This module is a pure `.ts` (no JSX) so the slice 069 vitest
// discipline (node env, `*.test.ts` only) stays honored; the layout
// JSX lives in the sibling `layout.tsx`.

import type { QueryClient } from "@tanstack/react-query";

import {
  getActivity,
  getControlDrift,
  getEvidenceFreshness,
  getFrameworkPosture,
  getMitigateRisks,
  getUpcoming,
} from "@/lib/api";

// DASHBOARD_QUERY_KEYS mirrors the queryKeys the page's six `useQuery`
// hooks register under (page.tsx lines ~54-77). The HydrationBoundary
// cache the layout dehydrates is only consumed by the page if these
// keys are byte-identical to the page's -- a drift here silently
// disables the prime and the client falls back to the cold BFF fetch.
// The page hard-codes its keys inline (a tiny `"use client"` module
// should not import server-helper consts); the cross-module drift guard
// is the equality assertion in dashboard-prefetch.test.ts.
export const DASHBOARD_QUERY_KEYS = {
  drift: ["dashboard", "drift"],
  freshness: ["dashboard", "freshness"],
  risks: ["dashboard", "risks"],
  upcoming: ["dashboard", "upcoming"],
  frameworkPosture: ["dashboard", "framework-posture"],
  activity: ["dashboard", "activity"],
} as const;

// E2E_NO_PREFETCH_COOKIE is the test-only cookie name the e2e harness
// sets to ask the dashboard layout to SKIP the SSR `Promise.all`
// prefetch, so the page falls back to its pure client-side `useQuery`
// path. This exists because the pre-existing `web/e2e/dashboard.spec.ts`
// intercepts panel data via Playwright `page.route(...)` (a BROWSER-side
// mock) and the slice-229 subtitle empty/error tests + the AC-7
// degrade-independently test depend on that interception reaching the
// initial render. The SSR prefetch is invisible to `page.route`; with
// the prefetch skipped, those mocks work exactly as before. The cookie
// is honored ONLY when `serverPrefetchBypassed()` is true (see below),
// so it is inert in production. See decisions log D6.
export const E2E_NO_PREFETCH_COOKIE = "e2e_no_prefetch";

// serverPrefetchBypassed reports whether the server is allowed to honor
// the E2E_NO_PREFETCH_COOKIE. It returns true ONLY when the process runs
// in test mode (`ATLAS_TEST_MODE=1`) -- the same env gate the Go-side
// `/v1/test/issue-jwt` endpoint uses (internal/api/testissuejwt.go),
// set by the CI Playwright job (.github/workflows/ci.yml) and the
// self-host test profile, and NEVER set in a production deployment.
// Reading `process.env` here (not `NEXT_PUBLIC_*`) keeps the flag
// server-only; it can never be inspected or forged from the browser.
export function serverPrefetchBypassed(): boolean {
  return process.env.ATLAS_TEST_MODE === "1";
}

// A single panel's server-side prefetch spec: the queryKey to seed and
// the bearer-taking upstream fn to call. The fn signatures intentionally
// match the `(bearer) => Promise<T>` shape `dashboardProxy` already
// consumes (lib/api.ts server-side fns), so the prefetch path reuses the
// SAME typed client as the BFF routes (AC-7) -- no reimplemented fetcher.
type PanelPrefetch = {
  readonly queryKey: readonly string[];
  readonly load: (bearer: string) => Promise<unknown>;
};

// DASHBOARD_PANEL_PREFETCHES enumerates the six panels. The order is not
// load-bearing (Promise.all fans out in parallel); it tracks the page's
// visual order for readability. `getMitigateRisks` returns the unwrapped
// `DashboardRisk[]` -- the same shape `fetchDashboardRisks` resolves to
// (it `.then(b => b.risks)`), so the dehydrated value matches the page's
// `risksQ.data` exactly (AC-6).
export const DASHBOARD_PANEL_PREFETCHES: readonly PanelPrefetch[] = [
  {
    queryKey: DASHBOARD_QUERY_KEYS.frameworkPosture,
    load: getFrameworkPosture,
  },
  { queryKey: DASHBOARD_QUERY_KEYS.risks, load: getMitigateRisks },
  { queryKey: DASHBOARD_QUERY_KEYS.freshness, load: getEvidenceFreshness },
  // getControlDrift's second arg (`since`) defaults to "7d" -- the same
  // default the client `fetchDashboardDrift` -> BFF -> getControlDrift
  // path uses -- so the bare fn reference (called with one arg) is
  // wire-equivalent to the client path (P0-4).
  { queryKey: DASHBOARD_QUERY_KEYS.drift, load: getControlDrift },
  { queryKey: DASHBOARD_QUERY_KEYS.upcoming, load: getUpcoming },
  { queryKey: DASHBOARD_QUERY_KEYS.activity, load: getActivity },
];

// prefetchDashboard fans out all six panels in parallel and seeds the
// supplied per-request QueryClient. Each panel's prefetch FAILS SOFT
// (D3): a bearer-less call or an upstream error skips that one query
// rather than rejecting the whole `Promise.all` and 500-ing the page.
// The client-side `useQuery` re-fetch (which the BFF routes still serve)
// is the post-hydration source of truth and corrects any skipped panel.
//
// `bearer` is `undefined` when the cookie is absent. We still run the
// fan-out so the (already-resolved) panels seed, but a `undefined`
// bearer makes every `load` throw upstream -> every panel fails soft ->
// the page renders cold and the (authed) layout's own redirect handles
// the unauthenticated case. We never fabricate data on a missing bearer.
export async function prefetchDashboard(
  queryClient: QueryClient,
  bearer: string | undefined,
): Promise<void> {
  if (!bearer) {
    return;
  }
  await Promise.all(
    DASHBOARD_PANEL_PREFETCHES.map(async ({ queryKey, load }) => {
      try {
        const data = await load(bearer);
        // `setQueryData` (NOT `prefetchQuery`) so we only seed the cache
        // on SUCCESS. `prefetchQuery` would catch the upstream error
        // internally and store an ERROR query-state into the dehydrated
        // cache -- the client `useQuery` would then hydrate into an
        // error state and skip its corrective re-fetch. Seeding only
        // successful data leaves a failed panel UNseeded, so the client
        // `useQuery` re-fetches it cold (D3) -- exactly the desired
        // fail-soft posture.
        queryClient.setQueryData([...queryKey], data);
      } catch {
        // Fail soft (D3): a single bad upstream must not abort the
        // whole fan-out, nor seed an error state. Leave this panel
        // unseeded; the client useQuery re-fetch corrects it.
      }
    }),
  );
}
