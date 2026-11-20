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

// upstreamErrorMessage is the text an error response shows the user. A 4xx
// is a refusal the platform words for the user (the approval rule, a frozen
// revision, "reason is required"), so its `{error}` body passes through. A
// 5xx never does: its body is the server's own failure, and a handler that
// wrote `err.Error()` there would leak internals. It gets `generic`, the
// status line unless the caller names another (#2433).
export async function upstreamErrorMessage(
  res: Response,
  generic = `${res.status} ${res.statusText}`,
): Promise<string> {
  if (res.status >= 500) return generic;
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    // body not JSON — keep the generic text
  }
  return upstreamErrorText(res.status, body, generic);
}

// upstreamErrorText is the same rule for a body the caller has already
// parsed (the AI-assist view-model parsers take `(ok, status, raw)`).
export function upstreamErrorText(
  status: number,
  body: unknown,
  generic: string,
): string {
  if (status >= 500) return generic;
  const e = (body as { error?: unknown } | null)?.error;
  return typeof e === "string" && e ? e : generic;
}

// bffControlFetch is the browser-side fetch against the BFF routes under
// `/api/**`. It unwraps a 4xx `{error}` JSON body into the thrown APIError
// message when present, otherwise falls back to the status line.
export async function bffControlFetch<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    throw new APIError(res.status, await upstreamErrorMessage(res));
  }
  return (await res.json()) as T;
}

// bffPostJSON posts to a BFF route and returns the parsed body, or throws
// an APIError carrying the platform's status and `{error}` message (409
// "already running", 400 "reason is required", ...). Moved here from
// control-imports.ts (#1778) so the remap review client shares it.
export async function bffPostJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    throw new APIError(
      res.status,
      await upstreamErrorMessage(res, `request failed (${res.status})`),
    );
  }
  return (await res.json().catch(() => ({}))) as T;
}
