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
  // Slice 399 — re-shaped the two assertions so they validate the
  // slice-146 NODE_ENV cookie behavior under the prod-build standalone
  // server (slice 387's `Frontend · Playwright e2e (prod-build
  // standalone)` job: build:standalone → `node
  // .next/standalone/web/server.js` → ATLAS_PROD_BUILD=1). Slice 387's
  // first real run of this spec exposed two dev-server-shaped assumptions
  // (they had never executed — the guard had never been satisfied until
  // 387 built the harness); 387 was forbidden from touching spec bodies
  // and filed the fix as slice 399. The re-shape, per slice 399's
  // decisions log:
  //   (1) Assertion 1 no longer asserts a client-side `/api/dashboard/`
  //       BFF-call COUNT (`bffResponses.length > 0`). Under the PRODUCTION
  //       build the dashboard panels are server-rendered (RSC fetches the
  //       data server-side during SSR), so zero client-side BFF calls
  //       fire. The slice-146 regression (cookie dropped on the BFF
  //       round-trip → proxy redirect to /login → JSON.parse of login HTML
  //       → "Unexpected token '<'") is now guarded RSC-aware: the page
  //       must render AUTHENTICATED (topbar "Sign out" present — proves the
  //       cookie survived the standalone-transport round-trip) AND the
  //       JSON-parse-HTML signature ("Unexpected token '<'") + the panel
  //       error Alert must be ABSENT. The content-type guard is retained
  //       but conditional: any `/api/dashboard/` response that IS observed
  //       must be application/json (no-ops when none fire).
  //   (2) Assertion 2's `context.addCookies` now derives the cookie domain
  //       from the Playwright `baseURL` (the served origin), not from
  //       `authedPage.url()` (which is `about:blank` before navigation →
  //       empty hostname → Playwright rejects the cookie). Same pattern
  //       fixtures.ts already uses for the bearer cookie.
  // The `test.skip(!ATLAS_PROD_BUILD)` guard is retained (387 D3): it
  // scopes the spec to the standalone leg and prevents green-washing
  // against the dev server. Runnable locally per the invocation block
  // above.
  test.skip(
    !RUN_AGAINST_PROD_BUILD,
    "ATLAS_PROD_BUILD not set — runs under the prod-build standalone server (slice 387 CI leg or locally with ATLAS_PROD_BUILD=1); skipped against the dev server to avoid green-washing the standalone-only path.",
  );

  authed(
    "dashboard renders authenticated (cookie survives), not the login HTML",
    async ({ authedPage }) => {
      // The fixture (web/e2e/fixtures.ts) has already injected the
      // session cookie against the baseURL origin. Under the prod build
      // the dashboard panels are server-rendered (RSC), so the data is
      // fetched server-side during SSR — the browser fires zero (or few)
      // client-side `/api/dashboard/` calls. We still capture any that DO
      // fire so the JSON-not-HTML content-type guard executes when a
      // browser-side BFF fetch exists; we do NOT require that one occurs.
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

      // (1) Authenticated render. If the slice-146 regression recurred,
      // the cookie would drop on the BFF round-trip under the standalone
      // server's plain-HTTP transport, the RSC fetch would fail auth, and
      // the standalone server would render the login page. The topbar
      // "Sign out" control is server-rendered only for an authenticated
      // session (web/components/shell/topbar.tsx), so its presence proves
      // the cookie survived the round-trip — the property slice 146 fixed.
      await expect(
        authedPage.getByRole("button", { name: "Sign out" }),
      ).toBeVisible();

      // (2) Regression signature absent. The classic slice-146 failure is
      // a panel JSON.parse of login HTML throwing "Unexpected token '<'",
      // surfaced in the panel error Alert ("Could not load this panel").
      // Assert that signature does not appear, and that no dashboard panel
      // is in its error state. Scoped to the regression signature, not a
      // blanket "no panel ever errors" — a thin seed producing an
      // unrelated panel error would not carry the HTML-parse string.
      await expect(authedPage.getByText("Unexpected token '<'")).toHaveCount(0);
      await expect(authedPage.locator('[data-testid$="-error"]')).toHaveCount(
        0,
      );

      // (3) Content-type guard, conditional. Any `/api/dashboard/`
      // response the browser DID observe must be application/json — an
      // HTML content-type is the exact regression signature (proxy.ts
      // redirected to /login and the fetch followed to login HTML). When
      // the prod build fires zero client-side calls this loop is a clean
      // no-op; it still guards the JSON-not-HTML property wherever a
      // browser-side BFF fetch exists.
      for (const r of bffResponses) {
        expect
          .soft(r.contentType, `${r.url} returned non-JSON`)
          .toContain("application/json");
      }
    },
  );

  authed(
    "session cookie sentinel never appears in browser-observable surfaces",
    async ({ authedPage, context, baseURL }) => {
      // Replace the fixture's cookie with one carrying our sentinel
      // value so we can prove the sentinel does not surface in any
      // log/response/console message during the dashboard render.
      // The sentinel is a neutral test string (P0-COOKIE-5); no
      // vendor-prefixed token prefix.
      //
      // Slice 399 — the cookie domain is derived from the Playwright
      // `baseURL` (the served origin), NOT from `authedPage.url()`. Before
      // any navigation `authedPage.url()` is `about:blank`, whose parsed
      // hostname is empty, so Playwright rejects the cookie ("Cookie
      // should have a url or a domain/path pair"). `baseURL` is always the
      // real served origin, so the cookie is set against a valid domain
      // regardless of navigation order — the same pattern fixtures.ts uses
      // for the bearer cookie.
      await context.clearCookies();
      const base = new URL(baseURL ?? "http://localhost:3000");
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
