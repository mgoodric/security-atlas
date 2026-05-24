// Slice 248 -- page-specific `<title>` metadata for `/settings`.
// Slice 249 -- server-side prefetch of `["settings-session-me"]` so the
//              SSR HTML ships the correct admin/non-admin variant on
//              the first byte instead of flickering through the
//              non-admin variant for ~50-200ms while the client-side
//              `useQuery(getSessionMe)` resolves.
//
// Why this file exists (slice 248 lineage):
// `web/app/(authed)/settings/page.tsx` is a client component
// (`"use client"` at the top). Next.js App Router forbids exporting
// `metadata` from a client component. The canonical pattern is a
// sibling server-component `layout.tsx` whose only job is to declare
// the page-specific metadata and pass `children` through unchanged.
//
// Slice 249 extends this layout to also serve as the SSR-prefetch
// surface. The layout:
//   1. Reads the `atlas_jwt` cookie server-side (auth.SESSION_COOKIE).
//   2. Fetches upstream `GET /v1/me` with that bearer (the same
//      upstream the BFF /api/admin/me route uses).
//   3. Projects the upstream JSON onto SessionMe via
//      `parseSessionMe` (fail-closed; see admin-prefetch.ts).
//   4. Seeds a per-request QueryClient's cache under the same
//      queryKey the page registers (`["settings-session-me"]`).
//   5. Wraps `children` in `<HydrationBoundary state={dehydrate(qc)}>`
//      so the client's `useQuery(["settings-session-me"], getSessionMe)`
//      boots with the prefetched value as initialData -- no flicker.
//
// Why a server component (no "use client"):
//   - We need `cookies()` from `next/headers`, which is server-only.
//   - We need to call upstream `/v1/me` server-side (bearer never
//     reaches the browser).
//   - HydrationBoundary is the canonical TanStack Query v5 SSR
//     primitive; it works from a server component.
//
// Why this layout, not page.tsx:
//   - page.tsx is `"use client"`; it cannot read cookies() or call
//     upstream URLs without a self-fetch to a BFF route (extra hop).
//   - Slice 248 already established the layout-as-server-component
//     pattern for this route; extending it composes cleanly.
//   - This is option (3) "initialData hydration via Next.js cache"
//     from the slice 249 spec; option (1) (cookie-decode + Server-
//     Component page) and option (2) (skeleton-until-hydrated) were
//     rejected -- see docs/audit-log/249-settings-admin-variants-
//     flicker-decisions.md for D1.
//
// Failure posture (P0-249-3 fail-closed):
//   - cookie absent          -> { is_admin: false } prefetched
//   - upstream non-200       -> { is_admin: false } prefetched
//   - upstream not-JSON      -> { is_admin: false } prefetched
//   - upstream fetch throws  -> { is_admin: false } prefetched
//   In every failure mode the SSR ships the non-admin variant; the
//   client-side `useQuery` re-fetch (P0-249-1) is the source of
//   truth post-hydration and corrects any drift.
//
// Privacy posture (P0-249-4 no cross-user cache):
//   `getQueryClient()` returns a FRESH QueryClient per server render
//   (see lib/queryClient.tsx -- `typeof window === "undefined"` branch
//   calls `makeQueryClient()` unconditionally). The browser-singleton
//   path is never exercised during SSR. Each user's prefetch is
//   isolated.
//
// See docs/audit-log/249-settings-admin-variants-flicker-decisions.md
// for D1 (Option 3 selected), D2 (failure modes), and D3 (per-request
// QueryClient).

import { HydrationBoundary, dehydrate } from "@tanstack/react-query";
import { cookies } from "next/headers";
import type { Metadata } from "next";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";
import { getQueryClient } from "@/lib/queryClient";

import {
  NON_ADMIN_SESSION_ME,
  SETTINGS_SESSION_ME_QUERY_KEY,
  parseSessionMe,
  type SessionMe,
} from "./admin-prefetch";

export const metadata: Metadata = {
  title: "Settings · security-atlas",
};

/**
 * fetchSessionMeServerSide reads the atlas_jwt cookie and calls
 * upstream `GET /v1/me` directly (not via the BFF route -- avoids an
 * unnecessary self-fetch hop). Returns the parsed SessionMe shape;
 * any failure path returns NON_ADMIN_SESSION_ME (P0-249-3).
 *
 * The function is intentionally `async` so the per-render
 * QueryClient.prefetchQuery can await it.
 */
async function fetchSessionMeServerSide(): Promise<SessionMe> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    // P0-249-3: no JWT -> non-admin variant. Do NOT fabricate a
    // role-claim from any client-side hint.
    return NON_ADMIN_SESSION_ME;
  }
  try {
    const upstream = await fetch(`${apiBaseURL()}/v1/me`, {
      headers: { Authorization: `Bearer ${bearer}` },
      // P0-249-4: never cache across users/requests. The
      // upstream answer depends on the bearer; reusing a cached
      // response would leak Tenant A's role into Tenant B's render.
      cache: "no-store",
    });
    if (upstream.status !== 200) {
      // Any non-200 (401, 403, 5xx) falls back to non-admin --
      // identical posture to the slice 060 BFF /api/admin/me route.
      return NON_ADMIN_SESSION_ME;
    }
    const json: unknown = await upstream.json();
    return parseSessionMe(json);
  } catch {
    // Network error / non-JSON body -> fail closed.
    return NON_ADMIN_SESSION_ME;
  }
}

export default async function SettingsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Per-request QueryClient (lib/queryClient.tsx returns a fresh
  // instance during SSR; P0-249-4).
  const queryClient = getQueryClient();

  // Prefetch the session-me query under the exact key the page's
  // useQuery registers. After dehydrate(), the client receives a
  // serialized cache snapshot that boots its QueryClient with the
  // value already populated -- the page's first paint reads it as
  // initialData and ships the correct admin/non-admin variant.
  await queryClient.prefetchQuery({
    queryKey: [...SETTINGS_SESSION_ME_QUERY_KEY],
    queryFn: fetchSessionMeServerSide,
  });

  return (
    <HydrationBoundary state={dehydrate(queryClient)}>
      {children}
    </HydrationBoundary>
  );
}
