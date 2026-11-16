// Server-side reads for /login and its server actions. Server-only:
// `apiBaseURL()` returns the internal atlas address only outside the
// browser (see lib/api/self.server.ts for the `.server.ts` convention).

import { apiBaseURL } from "@/lib/api/base";

import type { InstallState } from "./login-tenant";

// fetchInstallState reads /v1/install-state. null means it could not be
// read; callers treat that as "not first install" (fail closed).
export async function fetchInstallState(): Promise<InstallState | null> {
  try {
    const response = await fetch(`${apiBaseURL()}/v1/install-state`, {
      // #1756: no-store, like the /api/install-state BFF. A cached
      // first-install answer could post a stale tenant id, or (#2062)
      // keep offering the token sign-in after the first sign-in.
      cache: "no-store",
      // Bound the wait so a wedged backend doesn't stall /login forever.
      signal: AbortSignal.timeout(3000),
    });
    if (!response.ok) return null;
    return (await response.json()) as InstallState;
  } catch {
    return null;
  }
}

// hasValidSession asks the platform whether the session cookie's bearer is
// still good. Only a 200 from /v1/me counts: an expired or revoked cookie
// must leave the visitor on /login rather than bounce them to a page that
// sends them straight back.
export async function hasValidSession(token: string): Promise<boolean> {
  try {
    const response = await fetch(`${apiBaseURL()}/v1/me`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
      signal: AbortSignal.timeout(3000),
    });
    return response.ok;
  } catch {
    return false;
  }
}
