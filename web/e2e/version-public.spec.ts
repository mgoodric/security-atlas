// Slice 092 — Playwright E2E asserting the /api/version exemption
// end-to-end through the running Next.js dev/prod server.
//
// The vitest unit test (web/proxy.test.ts) verifies the proxy
// function's logic in isolation. This spec is the live verification
// (same belt-and-suspenders pattern as slice 086's open-redirect
// fix): drive a real HTTP request against the Playwright-managed
// server and confirm the BFF route returns 200 JSON, not the 307 the
// production bug was producing.
//
// AC-6: /api/version reachable without sign-in -> HTTP 200 with JSON.
// AC-7: /api/admin/me still gated -> HTTP 307 to /login.
//
// Hard rule (P0-A9 from slice 069): no vendor-prefixed token strings.

import { expect, test } from "@playwright/test";

test.describe("public /api/version reachability (slice 092)", () => {
  test("/api/version returns 200 JSON without a session cookie", async ({
    request,
  }) => {
    const res = await request.get("/api/version", {
      // Force the request to NOT follow redirects so a regression (307
      // back to /login) surfaces as a status-code assertion failure
      // rather than a follow-the-redirect 200 from /login.
      maxRedirects: 0,
    });

    expect(res.status()).toBe(200);
    expect(res.headers()["content-type"] ?? "").toContain("application/json");
    // The BFF route sets a 5-minute public cache header — see the
    // route.ts comment. P0-A2 forbids changing this value.
    expect(res.headers()["cache-control"]).toBe("public, max-age=300");

    // Body shape: the four-field contract slice 072 froze.
    const body = (await res.json()) as {
      version?: string;
      commit?: string;
      build_time?: string;
      go_version?: string;
    };
    expect(typeof body.version).toBe("string");
    expect(typeof body.commit).toBe("string");
    expect(typeof body.build_time).toBe("string");
    expect(typeof body.go_version).toBe("string");
  });

  test("/api/admin/me still redirects unauthenticated requests (AC-7)", async ({
    request,
  }) => {
    const res = await request.get("/api/admin/me", {
      maxRedirects: 0,
    });

    // The proxy redirects pre-route-handler with a 307 to /login.
    // (The route handler itself would return JSON 401 if it ran, but
    // the proxy fires first.)
    expect(res.status()).toBe(307);
    expect(res.headers()["location"] ?? "").toContain("/login");
    expect(res.headers()["location"] ?? "").toContain(
      "from=%2Fapi%2Fadmin%2Fme",
    );
  });
});
