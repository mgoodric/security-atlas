// Slice 060 — AC-10 Playwright E2E for the admin bootstrap flow.
//
// Seeded by fixtures/e2e/admin-bootstrap.sql: a tenant of the spec's own
// with an admin member, a viewer member, one off and one on feature flag,
// and no SSO config (the fixture resets the flags and the config on every
// run). Each test signs in with a JWT minted by /v1/test/issue-jwt for
// one of the two members. #2755 restored the assertions slice 060 left
// commented out behind the slice-082 quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/admin-bootstrap.spec.ts
//
// The test asserts the bootstrap flow described in AC-10:
//   1. An admin signs in
//   2. Visits /admin and sees the five tiles
//   3. Visits /admin/sso and runs the discovery preflight against a
//      discovery document the test serves through `page.route` (the
//      preflight is a browser-side fetch, so no real IdP is contacted)
//   4. Toggles a feature flag (off → on via the confirm modal)
//   5. Issues an API key and confirms the bearer plaintext shows once
//   6. Visits /admin/users and confirms the role-permission matrix
//      renders

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the tenant and members in fixtures/e2e/admin-bootstrap.sql.
const TENANT = "00000000-0000-0000-0000-2755a0000001";
const ADMIN_USER = "44444444-4444-4444-4444-2755a0000101";
const VIEWER_USER = "44444444-4444-4444-4444-2755a0000102";
// A registered flag that defaults off; the fixture deletes any override.
const OFF_FLAG = "vendor.claims";
// The issuer the preflight checks; `page.route` serves its discovery doc.
const PREFLIGHT_ISSUER = "https://idp.example.com";

async function signInAsAdmin(page: Page): Promise<void> {
  await signInToTenant(page, {
    tenantId: TENANT,
    userId: `user:${ADMIN_USER}`,
    roles: ["admin"],
    superAdmin: true,
  });
}

async function signInAsViewer(page: Page): Promise<void> {
  await signInToTenant(page, {
    tenantId: TENANT,
    userId: `user:${VIEWER_USER}`,
    roles: ["viewer"],
  });
}

test.describe("admin bootstrap", () => {
  // The fixture resets the flag and SSO state the flow changes. Serial
  // mode keeps every test on one worker, so no second worker's beforeAll
  // reseeds the tenant while the flow is mid-toggle.
  test.describe.configure({ mode: "serial" });

  test.beforeAll(() => {
    seedFromFixture("admin-bootstrap");
  });

  test("admin bootstrap end-to-end", async ({ page }) => {
    // The flow walks six pages; give it room beyond the 30s default.
    test.setTimeout(120_000);

    // 1. Sign in as the tenant's admin.
    await signInAsAdmin(page);

    // 2. Land on /admin overview.
    await page.goto("/admin");
    await expect(
      page.getByRole("heading", { level: 1, name: "Admin" }),
    ).toBeVisible({ timeout: 30_000 });
    for (const [href, tile, description] of [
      ["/admin/sso", "SSO", "OIDC identity-provider configuration"],
      ["/admin/users", "Users", "List users, assign roles"],
      ["/admin/api-keys", "API keys", "Issue, rotate, and revoke"],
      ["/admin/features", "Features", "Toggle feature flags per tenant"],
      ["/admin/audit", "Audit log", "Paginated read across"],
    ] as const) {
      // A tile is a link wrapping a card: its title, then its description.
      // The admin section nav's plain links to the same pages carry only
      // the title, so the description tells the tile apart.
      const tileLink = page
        .locator(`main a[href="${href}"]`)
        .filter({ hasText: description });
      await expect(tileLink).toBeVisible();
      await expect(tileLink).toContainText(tile);
    }

    // 3. SSO discovery preflight + save (slice 063 extension).
    await page.route(
      `${PREFLIGHT_ISSUER}/.well-known/openid-configuration`,
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          headers: { "Access-Control-Allow-Origin": "*" },
          body: JSON.stringify({
            issuer: PREFLIGHT_ISSUER,
            authorization_endpoint: `${PREFLIGHT_ISSUER}/authorize`,
            token_endpoint: `${PREFLIGHT_ISSUER}/token`,
            jwks_uri: `${PREFLIGHT_ISSUER}/jwks`,
          }),
        }),
    );
    await page.goto("/admin/sso");
    await page.fill("input#preflight-issuer", PREFLIGHT_ISSUER, {
      timeout: 30_000,
    });
    await page.getByRole("button", { name: "Run preflight" }).click();
    await expect(page.getByText("Discovery OK")).toBeVisible();
    await expect(page.getByText(`${PREFLIGHT_ISSUER}/token`)).toBeVisible();

    // 4. Toggle a feature flag off → on through the confirm modal.
    await page.goto("/admin/features");
    const flagRow = page.getByTestId(`flag-row-${OFF_FLAG}`);
    await expect(flagRow).toContainText("off", { timeout: 30_000 });
    await page.getByTestId(`flag-enable-${OFF_FLAG}`).click();
    const confirm = page.getByRole("dialog");
    await expect(confirm).toContainText(`Enable ${OFF_FLAG}?`);
    await confirm.getByRole("button", { name: "Confirm enable" }).click();
    await expect(confirm).toHaveCount(0);
    await expect(page.getByTestId(`flag-enable-${OFF_FLAG}`)).toHaveCount(0);
    await expect(page.getByTestId(`flag-actions-${OFF_FLAG}`)).toBeVisible();
    await expect(flagRow).toContainText("on");

    // 5. Issue an API key + confirm write-once disclosure.
    await page.goto("/admin/api-keys");
    await page.getByTestId("issue-label").fill("e2e admin bootstrap", {
      timeout: 30_000,
    });
    await page.getByTestId("issue-credential-submit").click();
    const callout = page.getByTestId("fresh-secret-callout");
    await expect(callout).toBeVisible();
    await expect(callout).toContainText("This is the only time");

    // 6. Verify the role-permission matrix lists the five roles.
    await page.goto("/admin/users");
    const matrix = page.locator("table").filter({
      has: page.getByRole("columnheader", { name: "Coarse permissions" }),
    });
    await expect(matrix).toBeVisible({ timeout: 30_000 });
    await expect(matrix.locator("tbody tr")).toHaveCount(5);
    for (const role of [
      "admin",
      "grc_engineer",
      "control_owner",
      "auditor",
      "viewer",
    ]) {
      await expect(matrix.getByTestId(`role-badge-${role}`)).toHaveText(role);
    }
  });

  // The e2e stack (the CI Playwright job and local bring-up) sets no
  // ATLAS_OIDC_SECRET_KEY, so PATCH /v1/admin/sso refuses any client
  // secret with 503 and a first config cannot be saved (#2034). #2773.
  test.fixme(
    "SSO config saves, re-renders without the secret, and keeps it on an empty re-save",
    async ({ page }) => {
      await signInAsAdmin(page);
      await page.goto("/admin/sso");

      // 3b. Fill the OIDC configuration form and submit (slice 063).
      await page.fill("input#sso-issuer-url", "https://idp.example.com", {
        timeout: 30_000,
      });
      await page.fill("input#sso-client-id", "platform-rp-client");
      await page.fill("input#sso-client-secret", "test-secret-placeholder");
      await page.fill(
        "input#sso-redirect-url",
        "https://your-deployment.example/auth/oidc/callback",
      );
      await page.fill("input#sso-allowed-domains", "example.com");
      await page.getByTestId("sso-save-button").click();
      await expect(page.getByTestId("sso-save-success")).toBeVisible();

      // 3c. Reload: the GET re-render shows the saved fields and does NOT
      //     show client_secret (slice 034 AC-9 / write-once).
      await page.reload();
      await expect(page.locator("input#sso-issuer-url")).toHaveValue(
        "https://idp.example.com",
        { timeout: 30_000 },
      );
      await expect(page.locator("input#sso-client-id")).toHaveValue(
        "platform-rp-client",
      );
      await expect(page.locator("input#sso-redirect-url")).toHaveValue(
        "https://your-deployment.example/auth/oidc/callback",
      );
      await expect(page.locator("input#sso-allowed-domains")).toHaveValue(
        "example.com",
      );
      // The backend never returns the secret; the UI never re-renders it.
      await expect(page.locator("input#sso-client-secret")).toHaveValue("");

      // 3d. Re-submit with an empty client_secret — the slice 062 contract
      //     treats this as "leave existing", so the save must succeed.
      await page.fill(
        "input#sso-allowed-domains",
        "example.com, sub.example.com",
      );
      await page.getByTestId("sso-save-button").click();
      await expect(page.getByTestId("sso-save-success")).toBeVisible();
      await page.reload();
      await expect(page.locator("input#sso-allowed-domains")).toHaveValue(
        "example.com, sub.example.com",
        { timeout: 30_000 },
      );
    },
  );

  test("non-admin sees 403 on /admin", async ({ page }) => {
    await signInAsViewer(page);
    const response = await page.goto("/admin");
    // The page exists; this user lacks the role. Not a 404.
    expect(response?.status()).toBe(200);
    await expect(page.getByText("This section is admin-only")).toBeVisible({
      timeout: 30_000,
    });
    // None of the admin tiles render behind the panel.
    await expect(page.locator('main a[href="/admin/sso"]')).toHaveCount(0);
  });

  // Slice 186 (F-178-6 closure) — AC-3: the sidebar "Admin" entry is
  // role-gated. With an admin session the entry IS visible; with a
  // non-admin session the entry is absent.
  test("admin bearer sees the sidebar Admin entry", async ({ page }) => {
    await signInAsAdmin(page);
    await page.goto("/dashboard");
    const adminLink = page.locator('aside nav a[href="/admin"]');
    await expect(adminLink).toBeVisible({ timeout: 30_000 });
    await expect(adminLink).toHaveText("Admin");
  });

  test("non-admin bearer does NOT see the sidebar Admin entry", async ({
    page,
  }) => {
    await signInAsViewer(page);
    await page.goto("/dashboard");
    // The other canonical entries DO render — the role-gate is surgical,
    // not blanket-hiding. Waiting on them first also proves the sidebar
    // has loaded before the absence check.
    for (const href of ["/dashboard", "/controls", "/risks", "/settings"]) {
      await expect(page.locator(`aside nav a[href="${href}"]`)).toBeVisible({
        timeout: 30_000,
      });
    }
    await expect(page.locator('aside nav a[href="/admin"]')).toHaveCount(0);
  });
});
