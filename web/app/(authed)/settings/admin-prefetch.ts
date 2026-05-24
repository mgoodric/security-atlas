// Slice 249 -- server-side admin-prefetch helpers for /settings.
//
// Why this module exists: `web/app/(authed)/settings/page.tsx` is a
// client component and resolves `is_admin` from
// `useQuery(["settings-session-me"], getSessionMe)`. On first SSR pass
// the query has not resolved, so `meQuery.data?.is_admin === undefined`
// and the page ships the NON-admin variant in the initial HTML; on
// hydration the client-side `/v1/me` resolves and the page swaps to
// the admin variant -- a visible "Admin role required" -> link flicker
// for the primary-user persona. See
// docs/issues/249-settings-admin-variants-flicker-on-first-paint.md
// for the visible regression and the three design options.
//
// D1 decision (slice 249): Option 3 -- initialData hydration via the
// sibling server-component layout (slice 248 pattern). The layout
// reads the `atlas_jwt` cookie, fetches upstream `/v1/me`, and seeds
// the page's `useQuery` initialData via `<HydrationBoundary>` so the
// SSR HTML already carries the correct variant. The client-side
// re-fetch (P0-249-1) stays in place as the source-of-truth post-
// hydration; this is a hydration-priming optimization, not a
// replacement.
//
// This module exports the pure logic the layout uses: parseSessionMe()
// reads an unknown upstream JSON value and projects it onto the same
// `SessionMe` shape the client's `getSessionMe()` returns. The
// fail-closed branch (P0-249-3) is identical in both paths: any
// shape we cannot prove is admin -> non-admin variant.
//
// The function is intentionally NOT a JSX file so the slice 069
// vitest discipline (node env, no React rendering, `*.test.ts` only)
// stays honored.

/**
 * SessionMe mirrors the client-side `SessionMe` shape from
 * `web/lib/api.ts` (line ~1901). Kept structurally identical so the
 * HydrationBoundary cache key + value are wire-compatible with the
 * client's `useQuery(["settings-session-me"], getSessionMe)`.
 */
export type SessionMe = {
  is_admin: boolean;
};

/**
 * parseSessionMe reads an unknown upstream JSON body (the value of
 * `await response.json()` from `/v1/me`) and projects it onto the
 * SessionMe shape with strict, fail-closed semantics.
 *
 * Fail-closed rules (P0-249-3):
 *   - `null` / non-object -> non-admin
 *   - missing `is_admin` field -> non-admin
 *   - `is_admin` is anything other than the literal boolean `true`
 *     -> non-admin (e.g. "true", 1, "yes", null all map to false)
 *
 * The narrow accept set ("only literal true is admin") is the same
 * shape the BFF route /api/admin/me uses (see route.ts line 77:
 * `const isAdmin = body.is_admin === true;`), so prefetch + client
 * re-fetch converge on identical decisions for any given upstream
 * payload.
 */
export function parseSessionMe(upstream: unknown): SessionMe {
  if (upstream === null || typeof upstream !== "object") {
    return { is_admin: false };
  }
  const body = upstream as { is_admin?: unknown };
  return { is_admin: body.is_admin === true };
}

/**
 * NON_ADMIN_SESSION_ME is the fail-closed default returned by the
 * prefetch path when:
 *   - the `atlas_jwt` cookie is absent (unauthenticated)
 *   - the upstream `/v1/me` returns non-200
 *   - the upstream body is not parseable JSON
 *   - the prefetch fetch throws (network error)
 *
 * Exported so the layout, tests, and any future call site share the
 * same constant -- a copy/paste drift here would silently widen the
 * admit set, the exact failure mode P0-249-3 guards against.
 */
export const NON_ADMIN_SESSION_ME: SessionMe = { is_admin: false };

/**
 * SETTINGS_SESSION_ME_QUERY_KEY is the queryKey the page's `useQuery`
 * registers under (see page.tsx line ~179:
 * `useQuery({ queryKey: ["settings-session-me"], queryFn: getSessionMe })`).
 *
 * Exported as a const so the layout's `prefetchQuery` cannot drift
 * from the page's `useQuery` -- if the page renames the key, this
 * export's call sites force a typecheck failure rather than a silent
 * cache miss (which would re-introduce the slice-249 flicker).
 */
export const SETTINGS_SESSION_ME_QUERY_KEY = ["settings-session-me"] as const;
