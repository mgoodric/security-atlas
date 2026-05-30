// Slice 370 — internal fetch helpers shared across the per-domain api
// modules. These were private (non-exported) helpers inside the former
// `web/lib/api.ts` god-file. They are exported here so the domain
// modules can import them, but the barrel `web/lib/api.ts` intentionally
// does NOT `export *` from this file — the underscore prefix marks it
// internal-to-package and preserves the god-file's public surface
// (these helpers were never part of `@/lib/api`'s public API).

import { apiBaseURL, APIError } from "./base";

// apiFetch is the server-side bearer fetch used by the RSC / BFF code
// path. The bearer comes from the session cookie (read server-side); the
// response is never cached (slice 380 P0-2 — a cached row would leak
// Tenant A's posture into Tenant B's render).
export async function apiFetch(
  path: string,
  bearer: string,
): Promise<Response> {
  const res = await fetch(`${apiBaseURL()}${path}`, {
    headers: {
      Authorization: `Bearer ${bearer}`,
    },
    // Slice 380 (P0-2): server-side fan-out prefetch (dashboard
    // layout.tsx) calls these typed fns directly during SSR. The
    // upstream answer depends on the bearer, so the response must never
    // enter the Next.js data cache — a cached row would leak Tenant A's
    // posture into Tenant B's render. Matches the BFF proxy's existing
    // `cache: "no-store"` (lib/api/bff.ts forwardJSON) so the direct and
    // proxied paths have identical caching posture.
    cache: "no-store",
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return res;
}

// bffControlFetch is the browser-side fetch against the BFF routes under
// `/api/**`. It unwraps an upstream `{error}` JSON body into the thrown
// APIError message when present, otherwise falls back to the status line.
export async function bffControlFetch<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}
