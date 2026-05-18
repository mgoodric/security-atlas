import { NextResponse, type NextRequest } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";

// Redirect unauthenticated traffic to /login. The login page itself is
// excluded so the user can land there to enter a token. Next.js 16
// renamed this convention from `middleware` to `proxy`.
//
// Slice 092: /api/version is the public version-metadata BFF route
// (web/app/api/version/route.ts — comment there documents the
// intentional-public contract). The proxy used to redirect it to
// /login, so the deployed UI's version-footer showed `v(none)`. The
// match below is exact-equality, NOT a prefix or regex, because P0-A1
// of slice 092 forbids broadening exemptions: a `startsWith("/api/v")`
// would silently expose `/api/vendors`, `/api/audit/period`, etc. The
// equality form fails closed if a sub-route is ever added.
//
// Slice 123: /api/install-state is added to the public-route exemption
// set. It is the BFF counterpart of the platform's intentionally-public
// `/v1/install-state` endpoint (consumed by the unauthenticated login
// page to decide whether to render the first-install guidance card).
// Exact-equality match per the P0-A1 discipline above.
//
// Slice 123: PUBLIC_STATIC_FILES is the explicit allow-list of static
// assets that live under web/public/ at the URL root and are referenced
// from the unauthenticated login page (favicon set + PWA icons + OG /
// Twitter unfurl cards + the two-theme logo SVG variants). Before this
// slice the proxy redirected requests for these assets to /login when
// no session cookie was present, so:
//   * page.request.get("/og-image.png") returned a 307 → 200 text/html
//     (the login page), breaking the slice-075 logo-render spec's
//     `expect(...).toBe("image/png")` assertion.
//   * Real OG scrapers fetching /og-image.png from a logged-out origin
//     would have received the login page HTML, breaking unfurls.
// The list is intentionally short + literal — broadening it via a
// regex (e.g. `\.(png|svg|ico)$`) would expose any future tenant-
// scoped asset under /something.png. New static assets that need to
// be public from the login page are added here explicitly.
const PUBLIC_STATIC_FILES = new Set<string>([
  "/icon-192.png",
  "/icon-512.png",
  "/apple-touch-icon.png",
  "/og-image.png",
  "/twitter-card.png",
  "/logo-light.svg",
  "/logo-dark.svg",
]);

// Slice 123: hardening headers applied to every Next.js response. The
// Go atlas backend has an equivalent middleware
// (internal/api/securityheaders/middleware.go, slice 087) — but every
// HTML response the browser sees on the dashboard is served by Next.js,
// not by atlas. Without this block, /login + /dashboard + every BFF
// route returned NO hardening headers, breaking the slice-087
// `security-headers.spec.ts` assertion + leaving the deployed UI
// clickjackable, MIME-sniffable, and Referer-leaky.
//
// Header values intentionally mirror the Go middleware's choices so the
// audit log entries that justified each directive (see
// docs/audit-log/087-security-http-headers-middleware-decisions.md)
// apply unchanged. CSP ships in report-only mode for the same
// hydration-violation reason — Next.js's inline hydration scripts would
// be blocked by an enforced `script-src 'self'`. The slice-087 D1
// decision applies identically here.
const CSP =
  "default-src 'self'; " +
  "img-src 'self' data:; " +
  "style-src 'self' 'unsafe-inline'; " +
  "script-src 'self'; " +
  "font-src 'self' data:; " +
  "frame-ancestors 'none'; " +
  "base-uri 'self'; " +
  "form-action 'self'";

const HSTS_MAX_AGE = "max-age=31536000; includeSubDomains";

function applySecurityHeaders(res: NextResponse): NextResponse {
  res.headers.set("Strict-Transport-Security", HSTS_MAX_AGE);
  res.headers.set("X-Content-Type-Options", "nosniff");
  res.headers.set("X-Frame-Options", "DENY");
  res.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  res.headers.set("Content-Security-Policy-Report-Only", CSP);
  return res;
}

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;

  if (
    pathname.startsWith("/login") ||
    pathname.startsWith("/_next") ||
    pathname === "/api/version" ||
    pathname === "/api/install-state" ||
    PUBLIC_STATIC_FILES.has(pathname)
  ) {
    return applySecurityHeaders(NextResponse.next());
  }

  const token = request.cookies.get(SESSION_COOKIE);
  if (!token?.value) {
    const url = request.nextUrl.clone();
    url.pathname = "/login";
    url.searchParams.set("from", pathname);
    return applySecurityHeaders(NextResponse.redirect(url));
  }
  return applySecurityHeaders(NextResponse.next());
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
