// OE-549 — server-side self-introspection probes, resolved against the
// platform's stable internal base instead of the app's own public origin.
//
// THE DEFECT THIS REPLACES. Six call sites across five server components
// reconstructed the app's own origin from the request Host header and
// fetched their OWN BFF route back through the public edge:
//
//     const host = h.get("host") ?? "localhost:3000";
//     const proto = h.get("x-forwarded-proto") ?? "http";
//     await fetch(`${proto}://${host}/api/admin/me`, ...);
//
// That address is unreachable from inside the web container for two
// INDEPENDENT reasons, either of which is sufficient to break it:
//
//   1. The Host header carries the EXTERNAL published port. A compose
//      mapping of `3001:3000` makes Host `localhost:3001`, and nothing
//      listens on 3001 inside the container -> ECONNREFUSED. The same
//      applies behind any reverse proxy (Host is the public hostname).
//   2. `next-server` in the production image binds the CONTAINER IP
//      (observed: 172.28.0.7:3000), not loopback — so even a Host that
//      names the correct port is refused when it resolves to 127.0.0.1.
//
// Symptom: every `/admin/*` page and `/audit-log` returned a 500 "This
// page couldn't load" (Next digest 3227098399) in the shipped container
// while passing in `next dev` and CI, where the server binds 0.0.0.0 and
// Host:port happens to match the internal port.
//
// THE FIX. Every OTHER page in the app already works because it reaches
// atlas through `apiBaseURL()` -> `ATLAS_HTTP_URL` (`http://atlas:8080`),
// a stable internal address that is independent of the published port,
// the reverse proxy, and the server's bind address. These probes now do
// the same, which also removes a pointless extra network hop: the BFF
// routes they used to call are thin bearer-forwarding proxies to the very
// same upstream endpoints.
//
// WIRE-SHAPE PARITY. The BFF routes under `/api/**` stay in place — the
// browser still calls them, and they are the only path that may reach the
// platform from a client component. The normalizers below are exported so
// the BFF route handler and these server-side probes share ONE definition
// of the response shape; a future edit to either normalizer moves both
// paths together and cannot silently diverge.
//
// FAIL-CLOSED POSTURE (unchanged, and now honoured on the network-error
// path too). Every probe collapses a non-2xx, a non-JSON body, or a
// transport failure to the DENY value: `{ is_admin: false, roles: [] }`
// for the admin gate, `{}` for enabled modules, `false` for demo-seed,
// `null` for the profile. The pre-OE-549 admin/audit-log gates threw on
// transport failure (hence the 500); denying is both the correct posture
// and strictly safer than crashing.
//
// SERVER-ONLY. `apiBaseURL()` returns the internal atlas address only
// when `typeof window === "undefined"`; in a browser it returns the
// same-origin default, which would send a raw bearer from client code.
// The `.server.ts` suffix marks the boundary for humans (same convention
// as `lib/feature-nav.server.ts`). Never import this from a client
// component.

import type { EnabledModules } from "@/lib/feature-nav";

import { apiBaseURL } from "./base";

/**
 * AdminMe is the normalized self-introspection shape. Mirrors the
 * `/api/admin/me` BFF wire contract (slice 060 `is_admin` + slice 130
 * `roles[]`) exactly — `roles` is ALWAYS an array, never absent.
 */
export type AdminMe = {
  is_admin: boolean;
  roles: string[];
};

/** emptyAdminMe is the deny value: not an admin, no roles. */
export function emptyAdminMe(): AdminMe {
  return { is_admin: false, roles: [] };
}

/**
 * normalizeAdminMe coerces an upstream `/v1/me` body into `AdminMe`.
 *
 * Fail-closed (slice 130 P0-A3): `is_admin` admits ONLY on a literal
 * `true`, and a missing / null / non-array `roles` collapses to `[]`
 * rather than being treated as permissive. Non-string entries inside a
 * `roles` array are dropped.
 *
 * Shared by the `/api/admin/me` BFF route handler and `fetchAdminMe`
 * below so the proxied and direct paths cannot drift apart.
 */
export function normalizeAdminMe(parsed: unknown): AdminMe {
  if (parsed === null || typeof parsed !== "object") {
    return emptyAdminMe();
  }
  const body = parsed as { is_admin?: unknown; roles?: unknown };
  return {
    is_admin: body.is_admin === true,
    roles: Array.isArray(body.roles)
      ? body.roles.filter((r): r is string => typeof r === "string")
      : [],
  };
}

/**
 * normalizeEnabledModules coerces an upstream `/v1/features/enabled`
 * body into the nav-gate map. Fail-closed: anything that is not an
 * object-valued `modules` field collapses to `{}`, and `gateNavItems`
 * reads a missing key as "off".
 *
 * Shared by the `/api/features/enabled` BFF route handler and
 * `fetchEnabledModules` below.
 */
export function normalizeEnabledModules(parsed: unknown): EnabledModules {
  if (parsed === null || typeof parsed !== "object") {
    return {};
  }
  const body = parsed as { modules?: unknown };
  if (typeof body.modules !== "object" || body.modules === null) {
    return {};
  }
  return body.modules as EnabledModules;
}

/**
 * fetchPlatformJSON is the single network primitive for these probes.
 * It targets `apiBaseURL()` — the internal atlas base — and NEVER the
 * app's own origin. Returns `null` on a non-2xx status, a non-JSON body,
 * or a transport error; the caller maps `null` to its deny value.
 *
 * `cache: "no-store"` matches `lib/api/_shared.ts apiFetch` and the BFF
 * proxy: the answer depends on the bearer, so a cached row would leak
 * one caller's roles into another's render (invariant #6).
 */
async function fetchPlatformJSON(
  path: string,
  bearer: string,
): Promise<unknown | null> {
  try {
    const res = await fetch(`${apiBaseURL()}${path}`, {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) {
      return null;
    }
    return (await res.json()) as unknown;
  } catch {
    return null;
  }
}

/**
 * fetchAdminMe reads the caller's own `is_admin` + `roles[]` from
 * `GET /v1/me`. Any signed-in bearer may call it — the platform reads
 * roles from `user_roles` under tenant RLS and never trusts a
 * client-supplied claim (slice 130 P0-A1).
 *
 * Replaces the self-fetch of the `/api/admin/me` BFF route, which is a
 * thin proxy to this same endpoint.
 */
export async function fetchAdminMe(bearer: string): Promise<AdminMe> {
  return normalizeAdminMe(await fetchPlatformJSON("/v1/me", bearer));
}

/**
 * SelfProfile is the subset of `GET /v1/me` the topbar avatar renders.
 * Kept `unknown`-typed so the caller's existing `asString` narrowing
 * stays the single validation point.
 */
export type SelfProfile = {
  display_name?: unknown;
  email?: unknown;
};

/**
 * fetchSelfProfile reads the operator's display name + email from
 * `GET /v1/me` for the topbar avatar. Returns `null` on any failure so
 * the avatar collapses to rendering nothing (slice 213: better a brief
 * gap than the wrong identity).
 *
 * Replaces the self-fetch of the `/api/me` BFF route.
 */
export async function fetchSelfProfile(
  bearer: string,
): Promise<SelfProfile | null> {
  const parsed = await fetchPlatformJSON("/v1/me", bearer);
  if (parsed === null || typeof parsed !== "object") {
    return null;
  }
  return parsed as SelfProfile;
}

/**
 * fetchEnabledModules reads the caller's tenant's gating flags from
 * `GET /v1/features/enabled` (authed, NOT admin-only). Fail-closed to
 * `{}` so every gated nav item hides rather than rendering a pre-GA link
 * the route would 404 on (slice 660).
 *
 * Replaces the self-fetch of the `/api/features/enabled` BFF route.
 */
export async function fetchEnabledModules(
  bearer: string,
): Promise<EnabledModules> {
  return normalizeEnabledModules(
    await fetchPlatformJSON("/v1/features/enabled", bearer),
  );
}

/**
 * fetchDemoSeedEnabled probes the admin demo-seed env-var gate via
 * `GET /v1/admin/demo/status` so the admin breadcrumb renders the "Demo"
 * link only when the feature is actually on (slice 278). The upstream is
 * admin-gated and answers 403 to a non-admin, which collapses to `false`.
 *
 * Replaces the self-fetch of the `/api/admin/demo/status` BFF route.
 */
export async function fetchDemoSeedEnabled(bearer: string): Promise<boolean> {
  const parsed = await fetchPlatformJSON("/v1/admin/demo/status", bearer);
  if (parsed === null || typeof parsed !== "object") {
    return false;
  }
  return (parsed as { enabled?: unknown }).enabled === true;
}
