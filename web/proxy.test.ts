// Slice 092 — vitest unit test for the Next.js 16 proxy request
// interceptor (proxy.ts, formerly middleware.ts pre-Next 16).
//
// The load-bearing assertion is P0-A1: the /api/version exemption MUST
// be exact-equality. The test enumerates the positive case (exempt),
// the AC-7 negative case (`/api/admin/me` still redirects), and the
// adjacent-prefix attack surface (`/api/vendors`, `/api/version2`,
// `/api/version/anything`) that a sloppy `startsWith("/api/v")` or
// `startsWith("/api/version")` would have leaked.
//
// Slice 123 extension — two new branches:
//
//   * /api/install-state is also exempt (matches the slice-073 BFF
//     contract for the unauthenticated login page; same
//     exact-equality discipline as /api/version).
//   * Every emitted response (next OR redirect) carries the five
//     hardening headers from the slice-087 middleware. The browser
//     sees a Next.js response, not the atlas response — without
//     these headers the deployed UI was clickjackable / MIME-
//     sniffable, and the slice-087 security-headers.spec.ts spec
//     was failing because the Next.js BFF never set them.
//   * web/public static assets referenced from /login (logo svgs,
//     PWA icons, OG/Twitter cards) are exempt — without this the
//     proxy redirected `GET /og-image.png` to `/login`, breaking the
//     slice-075 logo-render spec's content-type assertions.
//
// Hard rule (slice 069 P0-A9): no vendor-prefixed token strings in
// fixtures. The token below is a neutral test-* string.

import { beforeEach, describe, expect, test, vi } from "vitest";

// Mock next/server with a minimal shape sufficient for proxy.ts. The
// real NextResponse class isn't importable outside a Next runtime; the
// public surface proxy.ts uses is NextResponse.next() and
// NextResponse.redirect(url), then mutates the returned object's
// `headers` via Headers.set(...). The mock returns a recognizable
// marker plus a real Headers instance so the slice-123 hardening
// header assertions exercise the real set() path.
type ProxyMarker =
  | { kind: "next"; headers: Headers }
  | { kind: "redirect"; url: URL; headers: Headers };

vi.mock("next/server", () => {
  return {
    NextResponse: {
      next: (): ProxyMarker => ({ kind: "next", headers: new Headers() }),
      redirect: (url: URL): ProxyMarker => ({
        kind: "redirect",
        url,
        headers: new Headers(),
      }),
    },
  };
});

import { proxy as proxyImpl } from "./proxy";

// Wrap proxy() so the call site has the test marker type rather than
// the production NextResponse type the mock substitutes for.
const proxy = proxyImpl as unknown as (req: unknown) => ProxyMarker;

// The five hardening header names slice 123 added to the proxy.
// Matches the slice-087 middleware exactly (intentional duplication —
// the Go middleware applies to atlas responses, the proxy applies to
// Next.js responses, and the browser only ever sees the latter).
const SECURITY_HEADERS = [
  "strict-transport-security",
  "x-content-type-options",
  "x-frame-options",
  "referrer-policy",
  "content-security-policy-report-only",
];

function assertSecurityHeaders(res: ProxyMarker) {
  for (const name of SECURITY_HEADERS) {
    expect(res.headers.get(name), `missing ${name}`).not.toBeNull();
  }
}

// Synthetic NextRequest. The proxy function only touches:
//   request.nextUrl.pathname
//   request.nextUrl.clone()
//   request.cookies.get(name)
// So this minimal shape suffices.
function makeRequest(opts: {
  pathname: string;
  search?: string;
  cookies?: Record<string, string>;
}) {
  const base = "http://localhost";
  const url = new URL(`${base}${opts.pathname}${opts.search ?? ""}`);
  const cookies = opts.cookies ?? {};
  return {
    nextUrl: Object.assign(url, {
      clone: () => new URL(url.href),
    }),
    cookies: {
      get: (name: string) =>
        name in cookies ? { value: cookies[name] } : undefined,
    },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any;
}

describe("proxy (Next.js 16 request interceptor)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  // ---- AC-5 / AC-6: /api/version is exempt from the auth gate ----
  test("exempts /api/version (unauth -> next, not redirect)", () => {
    const res = proxy(makeRequest({ pathname: "/api/version" }));
    expect(res.kind).toBe("next");
  });

  test("exempts /api/version even when no session cookie is present", () => {
    // Belt-and-suspenders: the early-return must fire BEFORE the cookie
    // check; an empty cookie jar must not flip the outcome.
    const res = proxy(makeRequest({ pathname: "/api/version", cookies: {} }));
    expect(res.kind).toBe("next");
  });

  // ---- P0-A1: the exemption is exact-equality, NOT a broadening prefix ----
  test("does NOT exempt /api/version2 (adjacent path that startsWith would leak)", () => {
    const res = proxy(makeRequest({ pathname: "/api/version2" }));
    expect(res.kind).toBe("redirect");
  });

  test("does NOT exempt /api/vendors (the canonical P0-A1 hazard)", () => {
    const res = proxy(makeRequest({ pathname: "/api/vendors" }));
    expect(res.kind).toBe("redirect");
  });

  test("does NOT exempt /api/version/anything (sub-route, not the public route)", () => {
    const res = proxy(makeRequest({ pathname: "/api/version/build" }));
    expect(res.kind).toBe("redirect");
  });

  test("does NOT exempt /api/audit/period (another tenant-scoped route)", () => {
    const res = proxy(makeRequest({ pathname: "/api/audit/period" }));
    expect(res.kind).toBe("redirect");
  });

  // ---- AC-7: an existing tenant-scoped route still gates ----
  test("redirects unauthenticated /api/admin/me to /login with from= query", () => {
    const res = proxy(makeRequest({ pathname: "/api/admin/me" }));
    expect(res.kind).toBe("redirect");
    if (res.kind === "redirect") {
      expect(res.url.pathname).toBe("/login");
      expect(res.url.searchParams.get("from")).toBe("/api/admin/me");
    }
  });

  // ---- Regression: the existing /login + /_next exemptions still work ----
  test("/login is exempt (existing exemption preserved)", () => {
    const res = proxy(makeRequest({ pathname: "/login" }));
    expect(res.kind).toBe("next");
  });

  test("/_next/static/x is exempt (existing exemption preserved)", () => {
    const res = proxy(makeRequest({ pathname: "/_next/static/foo.js" }));
    expect(res.kind).toBe("next");
  });

  // ---- Regression: authed users pass through to non-exempt routes ----
  test("authenticated user passes through to a tenant route", () => {
    const res = proxy(
      makeRequest({
        pathname: "/dashboard",
        cookies: { atlas_jwt: "test-bearer-fixture" },
      }),
    );
    expect(res.kind).toBe("next");
  });

  // ---- Slice 123: /api/install-state is exempt (BFF for the
  //      unauthenticated login page's first-install detection) ----
  test("exempts /api/install-state (BFF for the public install-state endpoint)", () => {
    const res = proxy(makeRequest({ pathname: "/api/install-state" }));
    expect(res.kind).toBe("next");
  });

  test("exempts /api/install-state even when no session cookie is present", () => {
    // The login page calls this BEFORE the user signs in. The exemption
    // must fire before the cookie check, same as /api/version.
    const res = proxy(
      makeRequest({ pathname: "/api/install-state", cookies: {} }),
    );
    expect(res.kind).toBe("next");
  });

  test("does NOT exempt /api/install-state/anything (sub-route)", () => {
    // Same P0-A1 discipline as /api/version: exact-equality, not
    // startsWith. A future /api/install-state/bootstrap-token route
    // (hypothetical) must NOT inherit the exemption.
    const res = proxy(makeRequest({ pathname: "/api/install-state/anything" }));
    expect(res.kind).toBe("redirect");
  });

  // ---- Slice 123: public static assets referenced from /login are
  //      exempt — without this the proxy redirected requests for
  //      /og-image.png etc. to /login, breaking the logo-render spec. ----
  test.each([
    ["/og-image.png"],
    ["/twitter-card.png"],
    ["/icon-192.png"],
    ["/icon-512.png"],
    ["/apple-touch-icon.png"],
    ["/logo-light.svg"],
    ["/logo-dark.svg"],
  ])("exempts %s (public static asset on the login page)", (path) => {
    const res = proxy(makeRequest({ pathname: path }));
    expect(res.kind).toBe("next");
  });

  test("does NOT exempt /og-image-suffix.png (adjacent path that startsWith would leak)", () => {
    // PUBLIC_STATIC_FILES is a Set with exact-equality lookup; an
    // adjacent name still gates.
    const res = proxy(makeRequest({ pathname: "/og-image-suffix.png" }));
    expect(res.kind).toBe("redirect");
  });

  // ---- Slice 123: every emitted response carries the five hardening
  //      headers. The atlas Go middleware (slice 087) only covers atlas
  //      responses; the Next.js BFF needs its own header pass. ----
  test("hardening headers are set on the next() path (public /login)", () => {
    const res = proxy(makeRequest({ pathname: "/login" }));
    expect(res.kind).toBe("next");
    assertSecurityHeaders(res);
    // Defensive: report-only mode ships, NOT enforced. An enforced CSP
    // would block Next.js inline hydration scripts. Slice-087 D1 applies.
    expect(res.headers.get("content-security-policy")).toBeNull();
  });

  test("hardening headers are set on the next() path (authed /dashboard)", () => {
    const res = proxy(
      makeRequest({
        pathname: "/dashboard",
        cookies: { atlas_jwt: "test-bearer-fixture" },
      }),
    );
    expect(res.kind).toBe("next");
    assertSecurityHeaders(res);
  });

  test("hardening headers are set on the redirect() path (unauth -> /login)", () => {
    // The redirect response also carries the headers — a browser
    // following a 307 would otherwise see one un-headered hop. Defense
    // in depth.
    const res = proxy(makeRequest({ pathname: "/dashboard" }));
    expect(res.kind).toBe("redirect");
    assertSecurityHeaders(res);
  });

  test("hardening headers are set on the exempt static-asset path", () => {
    // OG scrapers, favicon fetchers, etc. also get the hardening
    // headers — defense in depth for the public surface.
    const res = proxy(makeRequest({ pathname: "/og-image.png" }));
    expect(res.kind).toBe("next");
    assertSecurityHeaders(res);
  });

  // ---- Slice 206: /v1/* and /metrics exemptions ----
  //
  // P0-A2 contract: these exemptions only remove the Next.js redirect-
  // to-login. The backend's slice-190 jwtmw middleware still gates the
  // request and returns 401 on missing auth. The exemption exists so an
  // unauthenticated curl to /v1/me surfaces the real backend 401 instead
  // of being masked by a Next.js 307 → /login.
  test.each([
    ["/v1/me"],
    ["/v1/anchors"],
    ["/v1/install-state"],
    ["/v1/oauth/token"],
  ])(
    "exempts %s (backend pass-through; backend still enforces auth)",
    (path) => {
      const res = proxy(makeRequest({ pathname: path }));
      expect(res.kind).toBe("next");
    },
  );

  test("exempts /metrics (slice-121 OTel runtime metrics endpoint)", () => {
    const res = proxy(makeRequest({ pathname: "/metrics" }));
    expect(res.kind).toBe("next");
  });

  test("does NOT exempt /metricsasdf (exact-equality on /metrics)", () => {
    // P0-A1-style discipline: a literal `/metrics` exemption must NOT
    // leak adjacent paths. A future /metrics-export route would NOT
    // inherit the exemption.
    const res = proxy(makeRequest({ pathname: "/metricsasdf" }));
    expect(res.kind).toBe("redirect");
  });

  test("does NOT exempt /v1 (no trailing slash; not the backend prefix)", () => {
    // /v1 alone is not the platform URL prefix — the platform mounts at
    // /v1/. Bare /v1 (no trailing /) is a 404 on the backend; keeping
    // it gated avoids masking the 404 with a 307 to /login but also
    // doesn't expand the exemption.
    const res = proxy(makeRequest({ pathname: "/v1" }));
    expect(res.kind).toBe("redirect");
  });

  // ---- Slice 206: the SESSION_COOKIE constant now resolves to
  //      `atlas_jwt`. The cookie-presence path uses the new name.
  test("authenticated user with atlas_jwt cookie passes through", () => {
    const res = proxy(
      makeRequest({
        pathname: "/dashboard",
        cookies: { atlas_jwt: "test-bearer-fixture" },
      }),
    );
    expect(res.kind).toBe("next");
  });

  test("legacy sa_session_token cookie no longer authenticates", () => {
    // Operator note: any browser still holding the pre-slice-206
    // `sa_session_token` cookie is treated as unauthenticated. They
    // bounce to /login once; the OAuth callback then sets atlas_jwt.
    const res = proxy(
      makeRequest({
        pathname: "/dashboard",
        cookies: { sa_session_token: "leftover-from-pre-slice-206" },
      }),
    );
    expect(res.kind).toBe("redirect");
  });
});
