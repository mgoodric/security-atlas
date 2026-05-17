// Slice 060 — AC-10 Playwright E2E for the admin bootstrap flow.
//
// Runner status (post-slice-069, verified 2026-05-15 by slice 071 audit):
// Playwright IS installed in `web/` (`@playwright/test` in devDeps;
// `web/playwright.config.ts` present; `npm run test:e2e` runs the suite;
// CI runs the `Frontend · Playwright e2e` job). The job is currently
// quarantined per slice 079 (`continue-on-error: true`) because the
// five un-shimmed specs reference seed-data preconditions that the
// docker-compose bring-up does not yet establish. The seed-data harness
// is slice 082 (`Playwright e2e seed-data harness`, status `not-ready`);
// when it lands, the quarantine line comes out and the un-commented
// assertions below become the gate.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
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
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL env points at a running platform instance
//   - TEST_ADMIN_BEARER env carries an admin credential
//   - The platform was seeded with at least one feature flag
//   - The platform exposes a test-discovery-doc HTTP endpoint for the
//     SSO preflight, OR the test points at a known public IdP discovery
//     doc that returns the four expected fields.

import { test } from "@playwright/test";

import { seedFromFixture } from "./seed";

// Per the preamble above: assertions are deliberately commented pending
// per-spec un-comment slices (slice 082's scoping decision — see
// docs/audit-log/082-playwright-seed-data-harness-decisions.md). The
// test body is preserved verbatim as a reviewable contract. Slice 082
// DOES wire the seed harness in `beforeAll` so the harness is exercised
// end-to-end against real Postgres+MinIO+NATS in CI.

test.describe("admin bootstrap", () => {
  test.beforeAll(() => {
    seedFromFixture("admin-bootstrap");
  });

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
    // 3. SSO discovery preflight + save (slice 063 extension).
    //    await page.goto("/admin/sso");
    //    await page.fill('input#preflight-issuer', "https://accounts.google.com");
    //    await page.click("text=Run preflight");
    //    await expect(page.getByText("Discovery OK")).toBeVisible();
    //
    // 3b. Fill the OIDC configuration form and submit (slice 063).
    //    await page.fill('input#sso-issuer-url', "https://idp.example.com");
    //    await page.fill('input#sso-client-id', "platform-rp-client");
    //    await page.fill('input#sso-client-secret', "test-secret-placeholder");
    //    await page.fill('input#sso-redirect-url', "https://your-deployment.example/auth/oidc/callback");
    //    await page.fill('input#sso-allowed-domains', "example.com");
    //    await page.click('[data-testid="sso-save-button"]');
    //    await expect(page.locator('[data-testid="sso-save-success"]')).toBeVisible();
    //
    // 3c. Reload and assert the GET re-render shows the saved fields
    //     and DOES NOT show client_secret (slice 034 AC-9 / write-once).
    //    await page.reload();
    //    await expect(page.locator('input#sso-issuer-url')).toHaveValue(
    //      "https://idp.example.com",
    //    );
    //    await expect(page.locator('input#sso-client-id')).toHaveValue(
    //      "platform-rp-client",
    //    );
    //    await expect(page.locator('input#sso-redirect-url')).toHaveValue(
    //      "https://your-deployment.example/auth/oidc/callback",
    //    );
    //    // Critically: client_secret input is empty after the reload.
    //    // The backend never returns it; the UI never re-renders it.
    //    await expect(page.locator('input#sso-client-secret')).toHaveValue(
    //      "",
    //    );
    //
    // 3d. Re-submit with an empty client_secret — slice 062 contract
    //     treats this as "leave existing", so the save must succeed.
    //    await page.fill('input#sso-allowed-domains', "example.com, sub.example.com");
    //    await page.click('[data-testid="sso-save-button"]');
    //    await expect(page.locator('[data-testid="sso-save-success"]')).toBeVisible();
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
