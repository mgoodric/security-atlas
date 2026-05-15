// Slice 087 — Playwright E2E for hardening HTTP headers.
//
// The middleware at internal/api/securityheaders/ sets five hardening
// headers on every response served through the chi chain. This spec is
// the browser-level regression: real requests to `/login` (public) and
// `/dashboard` (authed via the slice-069 fixture) must carry all five
// headers.
//
// Header-presence assertions only. Value validation lives in
// internal/api/securityheaders/middleware_test.go — duplicating it here
// would couple two test suites to the exact directive string and slow
// future tightening of the CSP. This spec catches "the middleware
// disappeared from the chain" regressions; the Go tests catch "the
// directives changed" regressions.
//
// Hard rule (slice-069 lesson, P0-A9): `expect` is imported AND used.
// No unused-import drift.
//
// Quarantine note: this spec runs under the post-079 Playwright
// quarantine. It executes only when the seed-data harness is wired up
// in CI (web/playwright.config.ts gates the e2e job behind a feature
// flag set by the slice that lands the seed harness).

import { test, expect } from "./fixtures";
import { test as anonTest, expect as anonExpect } from "@playwright/test";

// The five header names slice 087 binds. The CSP variant is
// report-only (Content-Security-Policy-Report-Only) per the decisions
// log §D1 — Next.js inline-script hydration violates a strict
// script-src 'self' enforcement today.
const SECURITY_HEADERS = [
  "strict-transport-security",
  "x-content-type-options",
  "x-frame-options",
  "referrer-policy",
  "content-security-policy-report-only",
];

anonTest.describe("security headers (public surfaces)", () => {
  anonTest(
    "login response carries all five hardening headers",
    async ({ page }) => {
      // Capture the main document response so we can read the response
      // headers directly. page.goto returns the navigation Response.
      const response = await page.goto("/login");
      anonExpect(response).not.toBeNull();

      const headers = response!.headers();
      for (const name of SECURITY_HEADERS) {
        anonExpect(
          headers[name],
          `missing ${name} on /login (slice 087 middleware regression)`,
        ).toBeDefined();
      }
      // Defensive sanity: the slice ships report-only, NOT enforced. An
      // enforced Content-Security-Policy on /login means a future slice
      // tightened the policy without updating the e2e expectation —
      // surface it loudly.
      anonExpect(
        headers["content-security-policy"],
        "unexpected enforced CSP on /login; slice 087 ships report-only — update this spec when CSP graduates",
      ).toBeUndefined();
    },
  );
});

test.describe("security headers (authed surfaces)", () => {
  test("dashboard response carries all five hardening headers", async ({
    authedPage,
  }) => {
    const response = await authedPage.goto("/dashboard");
    expect(response).not.toBeNull();

    const headers = response!.headers();
    for (const name of SECURITY_HEADERS) {
      expect(
        headers[name],
        `missing ${name} on /dashboard (slice 087 middleware regression)`,
      ).toBeDefined();
    }
  });
});
