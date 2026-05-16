// Slice 091 — Playwright E2E asserting the root-route redirect.
//
// `/` is a pure server-side redirect (no rendered UI). Two cases:
//   (a) Unauthenticated request  → /login?from=/   (307)
//   (b) Authenticated request    → /dashboard       (307)
//
// We use the page's request context with `maxRedirects: 0` so the 307
// response is observable directly (Playwright's navigation otherwise
// follows the redirect transparently and we'd assert against the
// destination URL — fine, but the AC text calls for the 307 itself in
// AC-4 / AC-2 / AC-3, so we exercise both: the raw 307 via the request
// API, and the followed-navigation destination via page.goto()).
//
// Auth posture: we sidestep the shared `authedPage` fixture here
// because it requires `TEST_BEARER` to be set — instead we inject a
// neutral fixture cookie value directly into the browser context for
// case (b). The server-side redirect only checks for cookie presence,
// not validity, so a fixture string is sufficient to exercise the
// branch. Real session validation happens downstream at `/dashboard`'s
// own auth gate (slice 034 + (authed) layout).
//
// Hard rule (P0-A7): neutral `test-*` fixture tokens only. NO
// vendor-prefixed strings (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) — even as
// placeholders — because GitGuardian scans test files.

import { expect, test } from "@playwright/test";

import { SESSION_COOKIE } from "../lib/auth";

test.describe("root-route redirect (slice 091)", () => {
  test("unauthenticated GET / returns 307 to /login?from=/", async ({
    request,
    baseURL,
  }) => {
    const res = await request.get(`${baseURL}/`, {
      maxRedirects: 0,
    });

    expect(res.status()).toBe(307);
    const location = res.headers()["location"];
    expect(location).toBeDefined();
    // Next.js may emit the redirect as a path-relative URL ("/login?from=/")
    // or absolute ("http://host/login?from=/"). Accept both shapes — the
    // load-bearing assertion is the path + query.
    const url = new URL(location, baseURL);
    expect(url.pathname).toBe("/login");
    expect(url.searchParams.get("from")).toBe("/");
  });

  test("authenticated GET / returns 307 to /dashboard", async ({
    browser,
    baseURL,
  }) => {
    const url = new URL(baseURL ?? "http://localhost:3000");
    const context = await browser.newContext();
    await context.addCookies([
      {
        name: SESSION_COOKIE,
        value: "test-session-fixture-091",
        domain: url.hostname,
        path: "/",
        httpOnly: true,
        secure: url.protocol === "https:",
        sameSite: "Lax",
      },
    ]);

    const res = await context.request.get(`${baseURL}/`, {
      maxRedirects: 0,
    });

    expect(res.status()).toBe(307);
    const location = res.headers()["location"];
    expect(location).toBeDefined();
    const dest = new URL(location, baseURL);
    expect(dest.pathname).toBe("/dashboard");
    // No `from=` propagation on the authed branch — it's a direct
    // landing, not a deferred destination.
    expect(dest.searchParams.has("from")).toBe(false);

    await context.close();
  });

  test("unauthenticated page.goto('/') lands on /login with from preserved", async ({
    page,
  }) => {
    // Belt-and-suspenders: ensure the navigation-following client sees
    // the expected destination, not the stock create-next-app template.
    await page.goto("/");
    await page.waitForURL(/\/login(\?|$)/, { timeout: 5_000 });

    const final = new URL(page.url());
    expect(final.pathname).toBe("/login");
    expect(final.searchParams.get("from")).toBe("/");
  });
});
