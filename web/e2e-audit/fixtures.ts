// Slice 178 — shared Playwright fixtures for the e2e-audit suite.
//
// Mirrors `web/e2e/fixtures.ts` structurally (slice 069 pattern) so the
// authentication surface is identical: TEST_BEARER (fast path) OR
// TEST_USER_EMAIL/TEST_USER_PASSWORD (real /auth/login flow). The
// fixture pre-authenticates the page AND patches it for read-only via
// `makeReadOnly()` so every spec inherits the AC-7 guardrail.
//
// Hard rule (P0-178-8 / slice 069 P0-A9): every token referenced here
// is a neutral test string. NO vendor-prefixed tokens.

import { test as base, expect, type Page } from "@playwright/test";

import { SESSION_COOKIE } from "../lib/auth";

import { makeReadOnly } from "./lib/make-read-only";
// Re-use the slice-082 seeded entity IDs for `:id` substitution.
import { DEMO_CONTROL_ID, DEMO_TENANT_ID } from "../e2e/seed";

type Fixtures = {
  authedReadOnlyPage: Page;
};

export const test = base.extend<Fixtures>({
  authedReadOnlyPage: async ({ page, baseURL }, use) => {
    const bearer = process.env.TEST_BEARER;
    const email = process.env.TEST_USER_EMAIL;
    const password = process.env.TEST_USER_PASSWORD;

    if (bearer) {
      const url = new URL(baseURL ?? "http://localhost:3000");
      await page.context().addCookies([
        {
          name: SESSION_COOKIE,
          value: bearer,
          domain: url.hostname,
          path: "/",
          httpOnly: true,
          secure: url.protocol === "https:",
          sameSite: "Lax",
        },
      ]);
    } else if (email && password) {
      const res = await page.request.post(`${baseURL}/auth/login`, {
        data: { email, password },
        headers: { "Content-Type": "application/json" },
      });
      if (!res.ok()) {
        throw new Error(
          `e2e-audit fixture: /auth/login returned ${res.status()}; check TEST_USER_EMAIL / TEST_USER_PASSWORD`,
        );
      }
    } else {
      throw new Error(
        "e2e-audit fixture: set TEST_BEARER or TEST_USER_EMAIL + TEST_USER_PASSWORD before running specs",
      );
    }

    // Patch the page in-place. Every subsequent click goes through the
    // mutation detector (AC-7, P0-178-1).
    makeReadOnly(page);

    // `use` here is the Playwright fixture callback (test.extend
    // contract — not a React hook). eslint's react-hooks plugin
    // false-positives because the identifier starts with `use`. Same
    // suppression pattern as web/e2e/fixtures.ts (slice 069) — but
    // that file is glob-excluded from eslint via the e2e/ exclude
    // pattern; this file is under a sibling path so the disable is
    // explicit here.
    // eslint-disable-next-line react-hooks/rules-of-hooks
    await use(page);
  },
});

export { expect };

/**
 * Resolve `:id` path segments to concrete seeded entity IDs. The
 * harness reads the manifest's route literally; this helper expands
 * any `:id` segments before the navigation.
 */
export function resolveRoute(route: string): string {
  if (route === "/controls/:id") {
    return `/controls/${DEMO_CONTROL_ID}`;
  }
  return route;
}

export const seeded = {
  tenantId: DEMO_TENANT_ID,
  controlId: DEMO_CONTROL_ID,
};
