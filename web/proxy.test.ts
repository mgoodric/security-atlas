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
// Hard rule (slice 069 P0-A9): no vendor-prefixed token strings in
// fixtures. The token below is a neutral test-* string.

import { beforeEach, describe, expect, test, vi } from "vitest";

// Mock next/server with a minimal shape sufficient for proxy.ts. The
// real NextResponse class isn't importable outside a Next runtime; the
// public surface proxy.ts uses is NextResponse.next() and
// NextResponse.redirect(url). Both return a recognizable marker so the
// tests can branch on type.
type ProxyMarker = { kind: "next" } | { kind: "redirect"; url: URL };

vi.mock("next/server", () => {
  return {
    NextResponse: {
      next: (): ProxyMarker => ({ kind: "next" }),
      redirect: (url: URL): ProxyMarker => ({ kind: "redirect", url }),
    },
  };
});

import { proxy as proxyImpl } from "./proxy";

// Wrap proxy() so the call site has the test marker type rather than
// the production NextResponse type the mock substitutes for.
const proxy = proxyImpl as unknown as (req: unknown) => ProxyMarker;

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
        cookies: { sa_session_token: "test-bearer-fixture" },
      }),
    );
    expect(res.kind).toBe("next");
  });
});
