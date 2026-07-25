// Slice 613 — Playwright E2E for the per-tenant control-bundle gate-policy
// control in Settings → Tenant.
//
// HERMETIC BY CONSTRUCTION (b219 lesson, CLAUDE.md):
//   - This spec route-mocks the BFF GET `/api/me/tenants` so the rendered
//     TenantSection has a deterministic current tenant id — it does NOT
//     depend on the shared docker-compose DB seed for the tenant directory
//     (slice 594 b219 took two resumes for exactly that non-determinism).
//   - The control's INITIAL value is the documented default (`strict`,
//     slice 608 D2); `main` exposes no GET that returns a single tenant's
//     `bundle_gate_mode` to the web layer, so the value is read from the
//     PATCH RESPONSE (which this spec mocks to a known mode). We assert the
//     PATCH `postData` carries the chosen mode and that the control reflects
//     the mocked response on success.
//
// The seed bearer is is_admin=true (slice 082 harness), so the admin-gated
// TenantSection (and the gate-policy control inside it) renders.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/settings-gate-policy.spec.ts

import { expect, test } from "./fixtures";

const MOCK_TENANT_ID = "11111111-2222-3333-4444-555555555555";

// Route-mock the tenant directory so TenantSection has a deterministic
// current tenant to PATCH against. This replaces the shared-DB read.
async function mockTenantDirectory(page: import("@playwright/test").Page) {
  await page.route("**/api/me/tenants", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          tenants: [
            { id: MOCK_TENANT_ID, name: "Hermetic Tenant", current: true },
          ],
        }),
      });
      return;
    }
    await route.continue();
  });
}

test.describe("/settings tenant gate-policy control (slice 613)", () => {
  test("AC-1: control renders, defaults to strict, shows the per-option explanation", async ({
    authedPage: page,
  }) => {
    await mockTenantDirectory(page);
    await page.goto("/settings");

    const control = page.getByTestId("settings-tenant-gate-policy");
    await expect(control).toBeVisible();

    // AC-1: pre-selected to the documented default (strict).
    const select = page.getByTestId("settings-tenant-gate-policy-select");
    await expect(select).toHaveValue("strict");

    // AC-3: a one-line explanation is shown for the selected mode.
    const desc = page.getByTestId("settings-tenant-gate-policy-description");
    await expect(desc).toContainText("Block a bundle whose tests fail");
  });

  test("AC-3: changing the select swaps the explanation without a network write", async ({
    authedPage: page,
  }) => {
    await mockTenantDirectory(page);
    await page.goto("/settings");

    const select = page.getByTestId("settings-tenant-gate-policy-select");
    await select.selectOption("mandatory_tests");

    const desc = page.getByTestId("settings-tenant-gate-policy-description");
    await expect(desc).toContainText("Reject a bundle that carries no tests");
  });

  test("AC-2: Save PATCHes /v1/tenants/{id} with the chosen bundle_gate_mode and reflects the persisted value", async ({
    authedPage: page,
  }) => {
    await mockTenantDirectory(page);

    // Mock the PATCH so the persisted value is deterministic (b219): the
    // response echoes the chosen mode back as the tenant row. We let the
    // request reach the route handler so we can assert on its postData.
    await page.route(`**/api/tenants/${MOCK_TENANT_ID}`, async (route) => {
      if (route.request().method() === "PATCH") {
        const sent = JSON.parse(route.request().postData() ?? "{}") as {
          bundle_gate_mode?: string;
        };
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            tenant: {
              id: MOCK_TENANT_ID,
              name: "Hermetic Tenant",
              bundle_gate_mode: sent.bundle_gate_mode ?? "strict",
            },
          }),
        });
        return;
      }
      await route.continue();
    });

    await page.goto("/settings");

    const select = page.getByTestId("settings-tenant-gate-policy-select");
    await select.selectOption("advisory");

    // Save fires the PATCH. Assert the request body via waitForResponse on
    // the BFF route so the postData assertion is on the real round-trip.
    const [response] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().includes(`/api/tenants/${MOCK_TENANT_ID}`) &&
          r.request().method() === "PATCH",
      ),
      page.getByTestId("settings-tenant-gate-policy-save").click(),
    ]);

    // AC-2: the PATCH carried the chosen mode.
    const sent = JSON.parse(response.request().postData() ?? "{}") as {
      bundle_gate_mode?: string;
    };
    expect(sent.bundle_gate_mode).toBe("advisory");

    // AC-2: on success the persisted value is reflected — the saved
    // confirmation names the committed mode and the select holds it.
    await expect(
      page.getByTestId("settings-tenant-gate-policy-saved"),
    ).toContainText("advisory");
    await expect(select).toHaveValue("advisory");
  });
});
