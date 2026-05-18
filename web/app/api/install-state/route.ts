// Slice 123 — BFF route for the public /v1/install-state endpoint.
//
// Mirrors slice 072's /api/version BFF: an intentionally-public
// upstream (atlas's GET /v1/install-state is bearer-exempt — see
// internal/api/httpserver.go and the slice-073 first-time-login UX)
// proxied through the BFF so the browser hits a same-origin URL and
// the Playwright e2e suite can intercept it via `page.route()`.
//
// Why a BFF and not a direct server-side fetch from the login page:
//
//   * Slice 073 originally fetched `/v1/install-state` from the
//     login page's Server Component using `apiBaseURL()`. That worked
//     at runtime but broke the slice-073 first-time-login.spec.ts
//     because Playwright's `page.route("**/v1/install-state")` only
//     intercepts BROWSER network requests — server-side `fetch()` from
//     an RSC happens in Node, never touches the browser, so the mock
//     never fires. The spec timed out waiting for the guidance card
//     under the real-atlas response.
//   * The BFF + client-side fetch pattern lets `page.route()` see the
//     request (the browser issues it), matches the slice-072 version
//     pattern, and keeps the upstream URL out of the client bundle.
//
// Anti-criterion P0-A5 (slice 073) preserved: a 5xx upstream falls
// through to `{first_install: false}` so the login form ALWAYS renders
// even when the metadata endpoint is broken.

import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";

export async function GET() {
  try {
    const upstream = await fetch(`${apiBaseURL()}/v1/install-state`, {
      cache: "no-store",
    });
    if (!upstream.ok) {
      // Upstream non-2xx -> treat as not-fresh. Matches the slice-073
      // login page fallback so a metadata failure never blocks sign-in.
      return NextResponse.json({ first_install: false }, { status: 200 });
    }
    const body = await upstream.text();
    return new NextResponse(body, {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        // No client cache — install-state flips exactly once over the
        // lifetime of a deployment, and a cached "first_install: true"
        // value would persist the FirstInstall card across subsequent
        // sign-ins until cache expiry. The TTL is "as long as the
        // login page is rendered", which is a single page-load — no
        // need for HTTP-layer caching.
        "Cache-Control": "no-store",
      },
    });
  } catch {
    // Network error talking to atlas -> treat as not-fresh, same as
    // the slice-073 fallback. The login form still renders.
    return NextResponse.json({ first_install: false }, { status: 200 });
  }
}
