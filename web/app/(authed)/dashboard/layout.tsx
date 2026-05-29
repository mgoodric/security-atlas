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
//   1. Reads the `atlas_jwt` cookie server-side (auth.SESSION_COOKIE).
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

import { SESSION_COOKIE } from "@/lib/auth";
import { getQueryClient } from "@/lib/queryClient";

import { prefetchDashboard } from "./dashboard-prefetch";

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Per-request QueryClient (getQueryClient returns a fresh instance
  // during SSR; no cross-user leak).
  const queryClient = getQueryClient();

  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;

  // Parallel fan-out: all six panels prefetched concurrently. Fails
  // soft per panel (D3).
  await prefetchDashboard(queryClient, bearer);

  return (
    <HydrationBoundary state={dehydrate(queryClient)}>
      {children}
    </HydrationBoundary>
  );
}
