// Slice 479 — Playwright E2E for the /admin/users management page.
// Slice 527 — extended to drive the user + tenant DROPDOWNS that replaced
// the raw-UUID text inputs.
//
// Assertions are driven by page.route() mocking of the BFF proxies at
// /api/admin/users (+ /revoke), /api/admin/tenants (slice 527), and
// /api/me. We mock the BFF rather than seed real data because the
// slice-478 cross-tenant assign writes platform-global rows under the
// BYPASSRLS pool — not something the Playwright harness can clean up
// between specs (same rationale as admin-tenants.spec.ts). The Go
// integration tests (478) assert the real behaviour against Postgres.
//
// Coverage:
//   (a) super_admin shape: cross-tenant list renders the Tenant column,
//       the "Add me to a tenant" button appears, an assign via the user +
//       tenant DROPDOWNS refreshes the list (AC-1, AC-2, AC-4, AC-5).
//   (b) self-assign uses the tenant dropdown and surfaces the re-auth
//       notice with a re-login link (AC-4 / AC-6 / P0-479-3 — no auto-switch).
//   (c) revoke flow has a confirm step (AC-6, unchanged).
//   (d) authz-honest: tenant-admin shape shows NO Tenant column and NO
//       self-assign button; in the assign dialog the tenant field is
//       PINNED (read-only), NOT a cross-tenant chooser, and the tenants
//       BFF is never called (AC-3 / AC-7 / P0-527-1); an upstream 403 on
//       assign surfaces inline, not a dead button (AC-7).
//   (e) a11y: the assign dialog dropdowns + role fieldset are labelled
//       (AC-8).

import { expect, test } from "./fixtures";

const TENANT_A = "11111111-1111-4111-8111-111111111111";
const TENANT_B = "22222222-2222-4222-8222-222222222222";
const USER_1 = "aaaaaaaa-1111-4111-8111-111111111111";
const USER_2 = "bbbbbbbb-2222-4222-8222-222222222222";

const SUPER_ROW_1 = {
  id: USER_1,
  tenant_id: TENANT_A,
  email: "alpha@example.com",
  display_name: "Alpha User",
  status: "active",
  roles: ["admin"],
};
const SUPER_ROW_2 = {
  id: USER_2,
  tenant_id: TENANT_B,
  email: "bravo@example.com",
  display_name: "Bravo User",
  status: "active",
  roles: ["viewer"],
};
const SUPER_ROW_NEW = {
  id: USER_1,
  tenant_id: TENANT_B,
  email: "alpha@example.com",
  display_name: "Alpha User",
  status: "active",
  roles: ["grc_engineer"],
};

const WITHIN_ROW = {
  id: USER_1,
  email: "alpha@example.com",
  display_name: "Alpha User",
  status: "active",
  roles: ["admin"],
};

// mockMe stubs /api/me so the within-tenant revoke fallback (and the
// slice-527 pinned tenant field) has a session tenant. Harmless for the
// cross-tenant specs.
async function mockMe(page: import("@playwright/test").Page, tenantId: string) {
  await page.route("**/api/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ tenant_id: tenantId }),
    }),
  );
}

// mockTenants stubs the slice-527 tenant-dropdown BFF. Returns a counter
// so a spec can assert the route was (or was NOT) called — the
// tenant-admin path must NOT fetch the cross-tenant list (P0-527-1).
function mockTenants(page: import("@playwright/test").Page) {
  const calls = { count: 0 };
  void page.route("**/api/admin/tenants", async (route) => {
    calls.count++;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            id: TENANT_A,
            name: "Tenant Alpha",
            slug: "tenant-alpha",
            is_bootstrap_tenant: true,
            created_at: "2026-05-22T00:00:00.000Z",
          },
          {
            id: TENANT_B,
            name: "Tenant Bravo",
            slug: "tenant-bravo",
            is_bootstrap_tenant: false,
            created_at: "2026-05-22T10:00:00.000Z",
          },
        ],
      }),
    });
  });
  return calls;
}

test.describe("admin users management page", () => {
  test("super_admin: lists cross-tenant users + supports assign", async ({
    authedPage,
  }) => {
    await mockMe(authedPage, TENANT_A);
    const tenantCalls = mockTenants(authedPage);
    let listCallCount = 0;
    await authedPage.route("**/api/admin/users", async (route) => {
      const req = route.request();
      if (req.method() === "GET") {
        listCallCount++;
        const items =
          listCallCount === 1
            ? [SUPER_ROW_1, SUPER_ROW_2]
            : [SUPER_ROW_1, SUPER_ROW_2, SUPER_ROW_NEW];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ items, cross_tenant: true }),
        });
        return;
      }
      if (req.method() === "POST") {
        const body = req.postDataJSON() as {
          user_id?: string;
          tenant_id?: string;
          roles?: string[];
          self_assign?: boolean;
        };
        expect(body.user_id).toBe(USER_1);
        expect(body.tenant_id).toBe(TENANT_B);
        expect(body.roles).toContain("grc_engineer");
        expect(body.self_assign).toBeFalsy();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            user_id: USER_1,
            tenant_id: TENANT_B,
            roles: ["grc_engineer"],
            idp_issuer: "urn:atlas:local",
            idp_subject: USER_1,
            membership_created: true,
          }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/users");

    // Cross-tenant table renders with a Tenant column.
    await expect(authedPage.getByTestId("users-table")).toBeVisible();
    await expect(authedPage.getByTestId(`user-row-${USER_1}`)).toBeVisible();
    await expect(authedPage.getByTestId("users-table")).toContainText("Tenant");
    // Self-assign button is visible for a super_admin.
    await expect(authedPage.getByTestId("open-self-assign")).toBeVisible();

    // Open assign dialog, choose user + tenant from the DROPDOWNS, submit.
    await authedPage.getByTestId("open-assign-user").click();
    await expect(authedPage.getByTestId("assign-user-dialog")).toBeVisible();
    // The user dropdown is populated from the already-loaded list (AC-1).
    await authedPage.getByTestId("assign-user-select").selectOption(USER_1);
    // The tenant dropdown is populated from GET /api/admin/tenants (AC-2).
    await authedPage.getByTestId("assign-tenant-select").selectOption(TENANT_B);
    await authedPage.getByTestId("assign-role-grc_engineer").click();
    await authedPage.getByTestId("assign-user-submit").click();

    // After refetch the new membership row appears.
    await expect(authedPage.getByTestId("users-table")).toContainText(
      "grc_engineer",
    );
    // The tenant dropdown was populated from the BFF (super_admin path).
    expect(tenantCalls.count).toBeGreaterThan(0);
  });

  test("super_admin: self-assign surfaces the re-auth notice (no auto-switch)", async ({
    authedPage,
  }) => {
    await mockMe(authedPage, TENANT_A);
    mockTenants(authedPage);
    await authedPage.route("**/api/admin/users", async (route) => {
      const req = route.request();
      if (req.method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            items: [SUPER_ROW_1, SUPER_ROW_2],
            cross_tenant: true,
          }),
        });
        return;
      }
      if (req.method() === "POST") {
        const body = req.postDataJSON() as { self_assign?: boolean };
        expect(body.self_assign).toBe(true);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            user_id: USER_1,
            tenant_id: TENANT_B,
            roles: ["admin"],
            idp_issuer: "urn:atlas:local",
            idp_subject: USER_1,
            membership_created: true,
          }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/users");
    await authedPage.getByTestId("open-self-assign").click();

    const dialog = authedPage.getByTestId("assign-user-dialog");
    await expect(dialog).toBeVisible();
    // Self-assign hides the user dropdown (the caller is the target).
    await expect(authedPage.getByTestId("assign-user-select")).toHaveCount(0);

    // A super_admin self-assigning still picks the target tenant via the
    // dropdown.
    await authedPage.getByTestId("assign-tenant-select").selectOption(TENANT_B);
    await authedPage.getByTestId("assign-role-admin").click();
    await authedPage.getByTestId("assign-user-submit").click();

    // The re-auth notice surfaces (AC-4); it links to the re-login flow.
    await expect(authedPage.getByTestId("reauth-notice")).toBeVisible();
    await expect(authedPage.getByTestId("reauth-tenant-id")).toContainText(
      TENANT_B,
    );
    await expect(authedPage.getByTestId("reauth-relogin-link")).toHaveAttribute(
      "href",
      "/login?from=/admin/users",
    );
  });

  test("super_admin: revoke has a confirm step", async ({ authedPage }) => {
    await mockMe(authedPage, TENANT_A);
    await authedPage.route("**/api/admin/users", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            items: [SUPER_ROW_1, SUPER_ROW_2],
            cross_tenant: true,
          }),
        });
        return;
      }
      await route.continue();
    });
    await authedPage.route("**/api/admin/users/revoke", async (route) => {
      const body = route.request().postDataJSON() as {
        user_id?: string;
        tenant_id?: string;
      };
      expect(body.user_id).toBe(USER_2);
      expect(body.tenant_id).toBe(TENANT_B);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ ok: true }),
      });
    });

    await authedPage.goto("/admin/users");
    await authedPage.getByTestId(`revoke-user-${USER_2}`).click();

    // The confirm dialog appears before any network call.
    await expect(authedPage.getByTestId("revoke-confirm-dialog")).toBeVisible();
    await authedPage.getByTestId("revoke-confirm-submit").click();
    // After success the dialog closes.
    await expect(authedPage.getByTestId("revoke-confirm-dialog")).toBeHidden();
  });

  test("authz-honest: tenant-admin sees no cross-tenant controls", async ({
    authedPage,
  }) => {
    await mockMe(authedPage, TENANT_A);
    const tenantCalls = mockTenants(authedPage);
    await authedPage.route("**/api/admin/users", async (route) => {
      if (route.request().method() === "GET") {
        // Within-tenant shape: no tenant_id on rows, cross_tenant=false.
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            items: [WITHIN_ROW],
            cross_tenant: false,
          }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/users");
    await expect(authedPage.getByTestId("users-table")).toBeVisible();
    // No self-assign (cross-tenant) control for a tenant-admin (P0-479-2).
    await expect(authedPage.getByTestId("open-self-assign")).toHaveCount(0);
    // The within-tenant table has no Tenant column.
    await expect(authedPage.getByTestId("users-table")).not.toContainText(
      "Tenant",
    );

    // Slice 527 (P0-527-1): in the assign dialog the tenant field is
    // PINNED (read-only) to the session tenant, NOT a cross-tenant chooser.
    await authedPage.getByTestId("open-assign-user").click();
    await expect(authedPage.getByTestId("assign-user-dialog")).toBeVisible();
    await expect(authedPage.getByTestId("assign-tenant-pinned")).toBeVisible();
    await expect(authedPage.getByTestId("assign-tenant-pinned")).toHaveValue(
      TENANT_A,
    );
    // There is NO tenant chooser dropdown for a tenant-admin.
    await expect(authedPage.getByTestId("assign-tenant-select")).toHaveCount(0);
    // And the cross-tenant tenants BFF was never called — the tenant-admin's
    // browser never receives the other-tenant names (STRIDE-I closed at the
    // fetch boundary).
    expect(tenantCalls.count).toBe(0);
  });

  test("authz-honest: an upstream 403 on assign surfaces inline (AC-7)", async ({
    authedPage,
  }) => {
    await mockMe(authedPage, TENANT_A);
    mockTenants(authedPage);
    await authedPage.route("**/api/admin/users", async (route) => {
      const req = route.request();
      if (req.method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            items: [SUPER_ROW_1],
            cross_tenant: true,
          }),
        });
        return;
      }
      if (req.method() === "POST") {
        await route.fulfill({
          status: 403,
          contentType: "application/json",
          body: JSON.stringify({
            error: "cross-tenant assignment requires super_admin",
          }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/users");
    await authedPage.getByTestId("open-assign-user").click();
    await authedPage.getByTestId("assign-user-select").selectOption(USER_1);
    await authedPage.getByTestId("assign-tenant-select").selectOption(TENANT_B);
    await authedPage.getByTestId("assign-role-viewer").click();
    await authedPage.getByTestId("assign-user-submit").click();

    // The 403 message renders inline — not a silent failure / dead button.
    await expect(authedPage.getByTestId("assign-user-error")).toBeVisible();
    await expect(authedPage.getByTestId("assign-user-error")).toContainText(
      "super_admin",
    );
  });

  test("a11y: assign dialog dropdowns + role fieldset are labelled (AC-8)", async ({
    authedPage,
  }) => {
    await mockMe(authedPage, TENANT_A);
    mockTenants(authedPage);
    await authedPage.route("**/api/admin/users", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            items: [SUPER_ROW_1],
            cross_tenant: true,
          }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/users");
    await authedPage.getByTestId("open-assign-user").click();

    // Inputs are associated with their <label htmlFor=...>.
    await expect(
      authedPage.locator('label[for="assign-user-id"]'),
    ).toBeVisible();
    await expect(
      authedPage.locator('label[for="assign-tenant-id"]'),
    ).toBeVisible();
    // The role group is a fieldset with a legend.
    await expect(authedPage.locator("fieldset legend")).toContainText("Roles");
    // Each role checkbox has its own label.
    await expect(
      authedPage.locator('label[for="assign-role-admin"]'),
    ).toBeVisible();
  });
});
