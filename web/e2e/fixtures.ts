// Slice 069 — shared Playwright fixtures for the e2e suite.
//
// Every spec in `web/e2e/*.spec.ts` needs an authenticated browser
// context targeting the platform-under-test. Without a shared fixture
// each spec would re-implement sign-in (and inevitably drift). This
// module exposes a single `test` and `expect` to import from, plus an
// `authedPage` fixture pre-signed-in.
//
// Auth modes (the fixture picks one per worker, in order):
//
//   1. `TEST_BEARER` env set
//        Fastest path: skip the login form and inject the bearer as the
//        session cookie directly. Local devs typically use this with a
//        long-lived test credential issued via `atlas-cli`.
//
//   2. `TEST_USER_EMAIL` + `TEST_USER_PASSWORD` env set
//        Real-flow path: POST the credentials to /auth/login, harvest
//        the Set-Cookie response, attach to the context. This is what CI
//        uses after the bootstrap container seeds the test user.
//
//   3. Neither set
//        The fixture throws. Specs that don't need auth (none today)
//        should use `chromium`'s plain page fixture, not `authedPage`.
//
// Hard rule (P0-A9): every token referenced here is a neutral test
// string. NO vendor-prefixed tokens (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`)
// even as placeholders — GitGuardian flags them in test files.

import { test as base, expect, type Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";

// Slice 082 — re-export seeded-entity accessors so specs reference
// rows by symbolic name (e.g. `seeded.controlId`) rather than embedded
// UUIDs in the spec body. The IDs are deterministic and match the
// fixture SQL files; renaming a row is then a single-file edit in
// `web/e2e/seed.ts`.
import {
  DEMO_AUDIT_PERIOD_ID,
  DEMO_CONTROL_ID,
  DEMO_FRAMEWORK_VERSION_ID,
  DEMO_TENANT_ID,
  DEMO_USER_EMAIL,
} from "./seed";

/**
 * SeededEntities is the typed surface specs use to reach into the
 * seeded data without hard-coding UUIDs. Add a field here when a new
 * row is added to `fixtures/walkthroughs/00-seed.sql` or
 * `fixtures/e2e/<spec>.sql` and the spec needs to reference it.
 */
export type SeededEntities = {
  tenantId: string;
  userEmail: string;
  controlId: string;
  frameworkVersionId: string;
  auditPeriodId: string;
};

/** Typed accessor for seeded entities. */
export const seeded: SeededEntities = {
  tenantId: DEMO_TENANT_ID,
  userEmail: DEMO_USER_EMAIL,
  controlId: DEMO_CONTROL_ID,
  frameworkVersionId: DEMO_FRAMEWORK_VERSION_ID,
  auditPeriodId: DEMO_AUDIT_PERIOD_ID,
};

type Fixtures = {
  authedPage: Page;
};

export const test = base.extend<Fixtures>({
  // Worker-scoped sign-in. Playwright instantiates `authedPage` per test
  // by default; we cache the cookie at worker scope by sharing the
  // browser context's storage state.
  authedPage: async ({ page, baseURL }, use) => {
    const bearer = process.env.TEST_BEARER;
    const email = process.env.TEST_USER_EMAIL;
    const password = process.env.TEST_USER_PASSWORD;

    if (bearer) {
      // Mode 1 — inject the bearer as the session cookie. The host is
      // derived from baseURL so the cookie matches the served domain.
      const url = new URL(baseURL ?? "http://localhost:3000");
      await page.context().addCookies([
        {
          name: ATLAS_JWT_COOKIE,
          value: bearer,
          domain: url.hostname,
          path: "/",
          httpOnly: true,
          secure: url.protocol === "https:",
          sameSite: "Lax",
        },
      ]);
    } else if (email && password) {
      // Mode 2 — real sign-in flow. The /auth/login endpoint sets the
      // session cookie; Playwright's `page.request` shares the
      // BrowserContext cookie jar, so a subsequent `page.goto` is
      // already authenticated.
      const res = await page.request.post(`${baseURL}/auth/login`, {
        data: { email, password },
        headers: { "Content-Type": "application/json" },
      });
      if (!res.ok()) {
        throw new Error(
          `e2e fixture: /auth/login returned ${res.status()}; check TEST_USER_EMAIL / TEST_USER_PASSWORD`,
        );
      }
    } else {
      throw new Error(
        "e2e fixture: set TEST_BEARER or TEST_USER_EMAIL + TEST_USER_PASSWORD before running specs",
      );
    }

    await use(page);
  },
});

export { expect };
