// Slice 660 — server-only enabled-modules read.
//
// Split from `feature-nav.ts` (pure/client-safe) because this module
// imports `next/headers`, which must never reach the client bundle. The
// authed shell's `getAuthedNav()` calls `fetchEnabledModules()` here and
// feeds the result to `gateNavItems` (in feature-nav.ts). Importing
// `next/headers` makes this module server-only at runtime (it throws if
// pulled into a client component); the `.server.ts` suffix documents the
// boundary for humans.

import { cookies, headers } from "next/headers";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import type { EnabledModules } from "@/lib/feature-nav";

/**
 * fetchEnabledModules reads the non-admin enabled-modules BFF
 * (`GET /api/features/enabled`) server-side. Self-referential fetch via
 * host + proto so the call resolves whether we render on the server or
 * behind the dev proxy (same pattern as the slice 186 admin probe).
 * Fail-closed: any error returns `{}` so every gated nav item collapses
 * to hidden — rendering a pre-GA nav link the route would 404 on is worse
 * than a brief absence.
 */
export async function fetchEnabledModules(): Promise<EnabledModules> {
  try {
    const jar = await cookies();
    const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
    if (!bearer) {
      return {};
    }
    const h = await headers();
    const host = h.get("host") ?? "localhost:3000";
    const proto = h.get("x-forwarded-proto") ?? "http";
    const res = await fetch(`${proto}://${host}/api/features/enabled`, {
      headers: { Cookie: `${ATLAS_JWT_COOKIE}=${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) {
      return {};
    }
    const body = (await res.json()) as { modules?: unknown };
    if (body && typeof body.modules === "object" && body.modules !== null) {
      return body.modules as EnabledModules;
    }
    return {};
  } catch {
    return {};
  }
}
