// Slice 660 — server-only enabled-modules read.
//
// Split from `feature-nav.ts` (pure/client-safe) because this module
// imports `next/headers`, which must never reach the client bundle. The
// authed shell's `getAuthedNav()` calls `fetchEnabledModules()` here and
// feeds the result to `gateNavItems` (in feature-nav.ts). Importing
// `next/headers` makes this module server-only at runtime (it throws if
// pulled into a client component); the `.server.ts` suffix documents the
// boundary for humans.

import { cookies } from "next/headers";

import { fetchEnabledModules as fetchEnabledModulesServer } from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import type { EnabledModules } from "@/lib/feature-nav";

/**
 * fetchEnabledModules reads the caller's tenant's gating flags
 * (`GET /v1/features/enabled` — authed, NOT admin-only) server-side.
 * Fail-closed: any error returns `{}` so every gated nav item collapses
 * to hidden — rendering a pre-GA nav link the route would 404 on is worse
 * than a brief absence.
 *
 * OE-549: this was a self-referential fetch of the app's own
 * `/api/features/enabled` BFF route at an origin rebuilt from the
 * request Host header. Inside the web container that address is refused
 * (the Host carries the EXTERNAL published port; next-server binds the
 * container IP, not loopback), so the probe fail-closed on EVERY authed
 * page and the gated nav entries were hidden regardless of the tenant's
 * real flags — the same defect that 500'd `/admin/*`, just fail-soft. It
 * now reads the platform through the stable internal base.
 */
export async function fetchEnabledModules(): Promise<EnabledModules> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return {};
  }
  return fetchEnabledModulesServer(bearer);
}
