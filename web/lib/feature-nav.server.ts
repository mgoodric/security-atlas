// Slice 660 / #2170 — server-only effective-modules read.
//
// Split from `feature-nav.ts` (pure/client-safe) because this module
// imports `next/headers`, which must never reach the client bundle. The
// authed shell's `getAuthedNav()` calls `fetchEffectiveModules()` here and
// feeds the result to `gateNavItems` (in feature-nav.ts). Importing
// `next/headers` makes this module server-only at runtime (it throws if
// pulled into a client component); the `.server.ts` suffix documents the
// boundary for humans.
//
// OE-549: the read goes to the platform through the stable internal base
// (`lib/api/self.server.ts`), never a self-fetch of the app's own origin.

import { cookies } from "next/headers";

import { fetchEffectiveModules as fetchEffectiveModulesServer } from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import type { EffectiveModules } from "@/lib/feature-nav";

/**
 * fetchEffectiveModules reads the caller's effective modules
 * (`GET /v1/me/modules`) server-side with the caller's own bearer.
 * `null` when there is no bearer or the read fails.
 */
export async function fetchEffectiveModules(): Promise<EffectiveModules> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return null;
  }
  return fetchEffectiveModulesServer(bearer);
}
