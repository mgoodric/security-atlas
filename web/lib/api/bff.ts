// Slice 042 — shared BFF forwarding helper for the audit workspace.
//
// Every audit BFF route does the same thing: read the bearer cookie,
// forward to the platform with an Authorization header, pass the
// upstream status + JSON body through verbatim. This helper centralizes
// that so each route file stays a thin declaration.
//
// Tenant derivation: the platform derives the tenant from the calling
// credential — these routes never pass a tenant_id (slice 051 D1). The
// credential is also what scopes the auditor to their assigned period
// (P0-1) and filters note visibility server-side (P0-2). The BFF adds no
// filtering of its own.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

type ForwardInit = {
  method?: string;
  // JSON body to forward. Omit for GET. For multipart use forwardMultipart.
  jsonBody?: unknown;
  // Extra request headers to forward (e.g. If-Match, #2311). They cannot
  // replace Authorization or Content-Type, which this helper sets.
  headers?: Record<string, string>;
};

// forwardJSON proxies a JSON request to the platform. Returns a
// NextResponse carrying the upstream status + body verbatim.
export async function forwardJSON(
  upstreamPath: string,
  init?: ForwardInit,
): Promise<NextResponse> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const headers: Record<string, string> = {
    ...init?.headers,
    Authorization: `Bearer ${bearer}`,
  };
  let body: string | undefined;
  if (init?.jsonBody !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(init.jsonBody);
  }
  const upstream = await fetch(`${apiBaseURL()}${upstreamPath}`, {
    method: init?.method ?? "GET",
    headers,
    body,
    cache: "no-store",
  });
  const text = await upstream.text();
  // Null-body statuses (204/205/304) reject any body in the Response
  // constructor — the saved-views DELETE upstream returns 204 with an empty
  // body, so forwarding the `""` text verbatim would throw. Pass null for
  // those statuses; the body is empty by definition either way. This is a
  // correctness guard, not a caching change.
  const hasNullBody =
    upstream.status === 204 ||
    upstream.status === 205 ||
    upstream.status === 304;
  return new NextResponse(hasNullBody ? null : text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

// noStore returns a copy of `res` with `Cache-Control: no-store` (and a
// legacy `Pragma: no-cache`) set, preserving the status, statusText, body,
// and every existing header. It is an OPT-IN wrapper a route handler
// applies to its own response — it does NOT change `forwardJSON`'s default
// behavior, so cache-friendly GET routes are unaffected (slice 746). Use it
// for per-user mutable resources that must never be browser-cached (e.g.
// saved views: a stale GET after a DELETE leaves the deleted row in place).
export function noStore(res: Response): Response {
  const headers = new Headers(res.headers);
  headers.set("Cache-Control", "no-store");
  headers.set("Pragma", "no-cache");
  // Null-body statuses (204/205/304) reject any body in the Response
  // constructor, so re-wrapping must pass `null` for them. The saved-views
  // DELETE upstream returns 204 — without this guard the wrapper (and the
  // body re-wrap) would throw. Other statuses carry `res.body` through.
  const nullBody =
    res.status === 204 || res.status === 205 || res.status === 304;
  return new Response(nullBody ? null : res.body, {
    status: res.status,
    statusText: res.statusText,
    headers,
  });
}

// forwardMultipart proxies a multipart/form-data request (walkthrough
// attachment upload) to the platform, streaming the FormData through.
export async function forwardMultipart(
  upstreamPath: string,
  form: FormData,
): Promise<NextResponse> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}${upstreamPath}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${bearer}` },
    body: form,
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** isUUID reports whether `v` is a canonical UUID. */
export function isUUID(v: string): boolean {
  return UUID_RE.test(v);
}

// rejectNonUUID returns a 400 when any path id is not a UUID, else null.
// A route that interpolates `[id]` into its upstream path calls it first:
// encodeURIComponent leaves `..` intact, so `/api/x/%2E%2E/verb` would
// otherwise normalise upstream to `/v1/verb` (security-atlas#2312).
export function rejectNonUUID(
  ids: Record<string, string>,
): NextResponse | null {
  for (const [name, v] of Object.entries(ids)) {
    if (!isUUID(v)) {
      return NextResponse.json(
        { error: `${name} must be a UUID` },
        { status: 400 },
      );
    }
  }
  return null;
}
