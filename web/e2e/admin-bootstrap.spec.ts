// Slice 060 — AC-10 Playwright E2E for the admin bootstrap flow.
//
// This spec lives ahead of the Playwright runner — as of slice 060,
// Playwright is NOT installed in `web/`. Adding @playwright/test +
// `web/playwright.config.ts` is the first follow-up after the admin
// section grows beyond stub state (i.e. when slice 060.5 lands the
// missing SSO / users / audit-log endpoints and we have a real
// end-to-end flow to assert).
//
// To run today (manual smoke):
//   cd web
//   npm install --save-dev @playwright/test
//   npx playwright install chromium
//   npx playwright test e2e/admin-bootstrap.spec.ts
//
// The test asserts the bootstrap flow described in AC-10:
//   1. Fresh admin user signs in
//   2. Visits /admin and sees the five tiles
//   3. Visits /admin/sso and runs the discovery preflight against the
//      ephemeral discovery document served from the test harness
//   4. Toggles a feature flag (off → on via the confirm modal)
//   5. Issues an API key and confirms the bearer plaintext shows once
//   6. Visits /admin/users and confirms the role-permission matrix
//      renders
//
// Pre-conditions to wire when Playwright is installed:
//   - PLATFORM_BASE_URL env points at a running platform instance
//   - TEST_ADMIN_BEARER env carries an admin credential
//   - The platform was seeded with at least one feature flag
//   - The platform exposes a test-discovery-doc HTTP endpoint for the
//     SSO preflight, OR the test points at a known public IdP discovery
//     doc that returns the four expected fields.

/* eslint-disable @typescript-eslint/no-unused-vars */

// The import is left commented so this file is syntactically valid
// TypeScript today without Playwright installed. CI's typecheck job
// ignores `web/e2e/**` via tsconfig include; ESLint ignores via
// eslint config. When Playwright lands, uncomment the import and
// remove the export-stub at the bottom.
//
// import { test, expect } from "@playwright/test";

type Test = (name: string, fn: () => Promise<void> | void) => void;
type Expect = <T>(actual: T) => {
  toBeVisible(): Promise<void>;
  toContainText(substr: string): Promise<void>;
  toHaveText(s: string | RegExp): Promise<void>;
};

declare const test: Test;
declare const expect: Expect;

function ifPlaywright(_fn: () => void) {
  // No-op shim until Playwright lands. Keeps this file as a static
  // contract describing the intended assertions.
}

ifPlaywright(() => {
  test("admin bootstrap end-to-end", async () => {
    // 1. Sign in with the test admin bearer.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_ADMIN_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.waitForURL("/dashboard");
    //
    // 2. Land on /admin overview.
    //    await page.goto("/admin");
    //    await expect(page.getByRole("heading", { name: "Admin" })).toBeVisible();
    //    for (const tile of ["SSO", "Users", "API keys", "Features", "Audit log"]) {
    //      await expect(page.getByText(tile, { exact: true })).toBeVisible();
    //    }
    //
    // 3. SSO discovery preflight.
    //    await page.goto("/admin/sso");
    //    await page.fill('input#issuer', "https://accounts.google.com");
    //    await page.click("text=Run preflight");
    //    await expect(page.getByText("Discovery OK")).toBeVisible();
    //
    // 4. Toggle a feature flag.
    //    await page.goto("/admin/features");
    //    const firstFlag = page.locator("text=Enable").first();
    //    await firstFlag.click();
    //    await expect(page.getByText("Confirm enable")).toBeVisible();
    //    await page.click("text=Confirm enable");
    //
    // 5. Issue an API key + confirm write-once disclosure.
    //    await page.goto("/admin/api-keys");
    //    await page.fill('input[placeholder^="{\\"connector\\"}"]', '{}');
    //    await page.click("text=Issue credential");
    //    const callout = page.locator('[data-testid="fresh-secret-callout"]');
    //    await expect(callout).toBeVisible();
    //    await expect(callout).toContainText("This is the only time");
    //
    // 6. Verify the role-permission matrix.
    //    await page.goto("/admin/users");
    //    for (const role of ["admin", "grc_engineer", "control_owner", "auditor", "viewer"]) {
    //      await expect(page.getByText(role)).toBeVisible();
    //    }
  });

  test("non-admin sees 403 on /admin", async () => {
    // 1. Sign in with a non-admin bearer (TEST_VIEWER_BEARER).
    // 2. Navigate to /admin.
    // 3. Assert the "This section is admin-only" alert renders, and the
    //    response was 200 (NOT a 404 — the page exists; this user lacks
    //    the role).
  });
});

// Export to keep this file a module (TS strict mode would otherwise
// complain about top-level declarations colliding across the e2e dir).
export {};
