// Slice 380 -- server-side fan-out prefetch layout for /dashboard.
// Closes slice 332 finding F-BFF-2 (MEDIUM).
//
// This layout is the SSR-prefetch surface for the program dashboard. It
// mirrors slice 249's `/settings` precedent (sibling server-component
// layout that prefetches the page's `useQuery` data and ships it via
// `HydrationBoundary`) and generalizes it from one query to a six-query
// parallel `Promise.all` fan-out.
//
// What it does:
//   1. Reads the `atlas_jwt` cookie server-side (auth.ATLAS_JWT_COOKIE).
//   2. Fans out all six dashboard panels' upstream fetches in parallel
//      (prefetchDashboard -> Promise.all) into a per-request QueryClient,
//      under the EXACT keys the page's six `useQuery` hooks register.
//   3. Wraps `children` in `<HydrationBoundary state={dehydrate(qc)}>`
//      so the page's `useQuery` hooks boot already-populated -- zero
//      client-side `/api/dashboard/**` BFF requests on first load
//      (AC-4/AC-8). The user pays one network round-trip: the HTML
//      document itself.
//
// Why a server component (no "use client"):
//   - `cookies()` from `next/headers` is server-only.
//   - The upstream fetches run server-side so the bearer never reaches
//     the browser (P0-3).
//   - `HydrationBoundary` is the canonical TanStack Query v5 SSR
//     primitive and works from a server component.
//
// Why a layout, not the page:
//   - The page is `"use client"` (it owns the six `useQuery` hooks +
//     the per-panel refresh affordance, AC-3/AC-5/AC-7). A client
//     component cannot read `cookies()` or call upstream URLs without a
//     self-fetch hop. Slice 249 established the layout-as-prefetch-
//     surface pattern; this extends it.
//
// Privacy posture (P0-3 / no cross-user cache):
//   `getQueryClient()` returns a FRESH QueryClient per server render
//   (lib/queryClient.tsx: the `typeof window === "undefined"` branch
//   calls `makeQueryClient()` unconditionally). The browser-singleton
//   path is never exercised during SSR, so each user's prefetch is
//   isolated. The upstream `apiFetch` sets `cache: "no-store"` (slice
//   380 hardening) so no tenant's posture is cached across requests
//   (P0-2).
//
// Failure posture (D3 fail-soft):
//   `prefetchDashboard` fails soft per panel -- a missing bearer or a
//   slow/failing upstream leaves that one panel UNseeded, so the
//   client `useQuery` re-fetches it cold. One bad upstream never 500s
//   the dashboard. The (authed) layout's own redirect (layout.tsx in
//   the (authed) group) handles the fully-unauthenticated case before
//   this layout's prefetch matters.
//
// See docs/audit-log/380-dashboard-server-component-fanout-decisions.md
// for D1 (slice-249 pattern over pure-RSC), D2 (per-panel TanStack state
// satisfies AC-2), D3 (fail-soft), D4 (no explicit Cache-Control header).

import { HydrationBoundary, dehydrate } from "@tanstack/react-query";
import { cookies } from "next/headers";
import type { Metadata } from "next";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getQueryClient } from "@/lib/queryClient";

// ATLAS-010 / AC-7 — page-specific `<title>` for /dashboard, matching the
// "<Page> · security-atlas" convention /settings established (slice 248).
export const metadata: Metadata = {
  title: "Dashboard · security-atlas",
};

import {
  E2E_NO_PREFETCH_COOKIE,
  prefetchDashboard,
  serverPrefetchBypassed,
} from "./dashboard-prefetch";

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Per-request QueryClient (getQueryClient returns a fresh instance
  // during SSR; no cross-user leak).
  const queryClient = getQueryClient();

  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;

  // Slice 380 e2e seam: the pre-existing dashboard.spec.ts intercepts
  // panel data via Playwright `page.route(...)` -- a BROWSER-side mock
  // that cannot reach the SSR `Promise.all` prefetch. When the e2e
  // harness sets the `e2e_no_prefetch` cookie AND the server runs in
  // test mode (ATLAS_TEST_MODE=1, set only by the CI Playwright job and
  // the self-host test profile), the layout SKIPS the SSR prefetch so
  // the page falls back to its pure client-side `useQuery` path --
  // restoring exactly the behavior those `page.route` mocks rely on.
  // The double gate (env AND cookie) means production can never trip
  // this even if an attacker forges the cookie -- ATLAS_TEST_MODE is
  // never "1" in a production deployment. See decisions log D6.
  const bypassPrefetch =
    serverPrefetchBypassed() && jar.get(E2E_NO_PREFETCH_COOKIE)?.value === "1";

  if (!bypassPrefetch) {
    // Parallel fan-out: all six panels prefetched concurrently. Fails
    // soft per panel (D3).
    await prefetchDashboard(queryClient, bearer);
  }

  return (
    <HydrationBoundary state={dehydrate(queryClient)}>
      {children}
    </HydrationBoundary>
  );
}
