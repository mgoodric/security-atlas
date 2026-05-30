// Slice 146 — Playwright E2E asserting the BFF cookie round-trips in
// the Next.js production-build standalone server (not just `next dev`).
//
// Why this spec exists: slice 132's engineer surfaced (decisions log
// D5) that dashboard / control-detail / audit-workspace / board-pack-
// preview panels render "Could not load this panel · Unexpected
// token '<'" under `node .next/standalone/server.js` over plain HTTP,
// even though the same surface works under `npm run dev`. The fix
// (web/lib/secure-cookie.ts) replaces a blunt
// `secure: process.env.NODE_ENV === "production"` cookie-attribute
// check with per-request transport detection. This spec is the live
// regression-prevention gate — without it, the same regression can
// recur on the next `NODE_ENV`-coupled cookie attribute that lands.
//
// Two assertions:
//
//   1. Authenticated browser visiting `/dashboard` receives BFF
//      panel JSON, NOT HTML. The classic failure-mode signature
//      is JSON.parse of the login HTML throwing "Unexpected token
//      '<'"; we assert the BFF returns an `application/json`
//      content-type instead of `text/html`.
//
//   2. The session cookie value (a planted neutral sentinel —
//      `test-cookie-sentinel-do-not-log-abcdef`, NOT a vendor-
//      prefixed token per P0-COOKIE-5) is not written into any
//      server log or response body the browser-visible spec can
//      observe. The assertion uses Playwright's console + response
//      listeners — server stdout/stderr is not directly observable
//      from the browser context, but the response-body capture
//      covers the BFF JSON path that the regression turned into
//      HTML, and the console capture covers any client-side
//      misbehavior that surfaces the cookie.
//
// Quarantine note (slice 082): this spec requires the Next.js
// production-build standalone server running against a real platform
// backend AND a known-good test bearer. The CI baseURL points at the
// docker-compose'd dev server (`npm run dev`), not the standalone
// output, so the spec is `test.skip()`-guarded behind ATLAS_PROD_BUILD
// to keep the always-on CI run a no-op. Local invocation:
//
//   cd web && npm run build && node .next/standalone/server.js &
//   ATLAS_PROD_BUILD=1 TEST_BEARER=... npx playwright test \
//     bff-cookie-production-build.spec.ts
//
// Once the slice-082 seed harness can provision a standalone server
// inside the CI matrix (separate slice), drop the guard.
//
// Hard rule (P0-COOKIE-5 from slice 146 + P0-A9 from slice 069):
// the cookie sentinel value is a neutral test string. NO vendor-
// prefixed token strings — GitGuardian scans test files too.

import { test } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { test as authed, expect } from "./fixtures";

const COOKIE_SENTINEL = "test-cookie-sentinel-do-not-log-abcdef";
const RUN_AGAINST_PROD_BUILD = !!process.env.ATLAS_PROD_BUILD;

test.describe("BFF cookie forwarding in production-build standalone", () => {
  // Slice 387 — the standalone CI harness now EXISTS (the
  // `Frontend · Playwright e2e (prod-build standalone)` job builds the
  // `output: "standalone"` bundle, boots `node
  // .next/standalone/web/server.js`, and sets ATLAS_PROD_BUILD=1). When
  // slice 387 first ran this spec against that server, two body
  // assumptions were exposed as dev-server-shaped (they had never run
  // before — the guard had never been satisfied):
  //   (1) "dashboard panel BFF returns JSON" asserts the browser fires
  //       `/api/dashboard/` BFF fetches. Under the PRODUCTION build the
  //       dashboard panels are server-rendered (RSC), so zero client-side
  //       BFF calls fire — the page renders fully authenticated (auth
  //       works; this is NOT the slice-146 cookie regression). The
  //       `bffResponses.length > 0` assertion is therefore false for the
  //       prod build and must be re-shaped (e.g. assert the panel content
  //       resolved, not that a client BFF call occurred).
  //   (2) "session cookie sentinel …" calls `context.addCookies` with
  //       `domain: new URL(authedPage.url()).hostname` BEFORE any
  //       navigation, so `authedPage.url()` is `about:blank` and the
  //       hostname is empty → Playwright rejects the cookie.
  // Fixing the body is out of slice 387's scope (which forbids touching
  // spec bodies) and is filed as slice 399
  // (docs/issues/399-bff-cookie-prod-build-spec-body-fix.md). Until 399
  // lands, the slice-387 CI leg runs ONLY the logo spec and holds this
  // one back; the guard below is retained unchanged. Runnable locally per
  // the invocation block above (it will surface the two issues described).
  test.skip(
    !RUN_AGAINST_PROD_BUILD,
    "ATLAS_PROD_BUILD not set — runs under the prod-build standalone server (slice 387 CI leg or locally with ATLAS_PROD_BUILD=1); skipped against the dev server to avoid green-washing the standalone-only path. Body re-shape tracked in slice 399.",
  );

  authed(
    "dashboard panel BFF returns JSON, not the login HTML",
    async ({ authedPage }) => {
      // The fixture (web/e2e/fixtures.ts) has already injected the
      // session cookie. Visit the dashboard so the React-Query panels
      // fire their browser-side fetches against /api/dashboard/**.
      const bffResponses: Array<{ url: string; contentType: string }> = [];
      authedPage.on("response", (response) => {
        const url = response.url();
        if (url.includes("/api/dashboard/")) {
          bffResponses.push({
            url,
            contentType: response.headers()["content-type"] ?? "",
          });
        }
      });

      await authedPage.goto("/dashboard", { waitUntil: "networkidle" });

      // Every BFF response we observed must be JSON. An HTML
      // content-type is the exact regression signature — the
      // proxy.ts redirected to /login and the browser fetch
      // followed the redirect to the login HTML.
      expect(bffResponses.length).toBeGreaterThan(0);
      for (const r of bffResponses) {
        expect
          .soft(r.contentType, `${r.url} returned non-JSON`)
          .toContain("application/json");
      }
    },
  );

  authed(
    "session cookie sentinel never appears in browser-observable surfaces",
    async ({ authedPage, context }) => {
      // Replace the fixture's cookie with one carrying our sentinel
      // value so we can prove the sentinel does not surface in any
      // log/response/console message during the dashboard render.
      // The sentinel is a neutral test string (P0-COOKIE-5); no
      // vendor-prefixed token prefix.
      await context.clearCookies();
      const base = new URL(authedPage.url() || "http://localhost:3000");
      await context.addCookies([
        {
          name: ATLAS_JWT_COOKIE,
          value: COOKIE_SENTINEL,
          domain: base.hostname,
          path: "/",
          httpOnly: true,
          secure: base.protocol === "https:",
          sameSite: "Lax",
        },
      ]);

      const consoleMessages: string[] = [];
      authedPage.on("console", (msg) => consoleMessages.push(msg.text()));

      const responseBodies: string[] = [];
      authedPage.on("response", async (response) => {
        // The platform returns 401 for this cookie (it's a bogus
        // bearer); we still capture the response body to assert the
        // sentinel doesn't echo back. We catch any body-read errors
        // because Playwright treats redirects/no-bodies as throws.
        try {
          const body = await response.text();
          responseBodies.push(body);
        } catch {
          /* ignore unreadable bodies */
        }
      });

      await authedPage.goto("/dashboard", { waitUntil: "networkidle" });

      // Sentinel must not appear in any console message.
      for (const m of consoleMessages) {
        expect
          .soft(m, "sentinel found in console message")
          .not.toContain(COOKIE_SENTINEL);
      }

      // Sentinel must not appear in any response body the browser
      // sees. (Server stdout/stderr is out-of-band for the browser
      // context; that surface is covered by reviewer discipline + the
      // sentinel naming convention which makes a leak grep-able.)
      for (const body of responseBodies) {
        expect
          .soft(body, "sentinel found in response body")
          .not.toContain(COOKIE_SENTINEL);
      }
    },
  );
});
